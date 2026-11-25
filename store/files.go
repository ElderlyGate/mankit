package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"go.naywee.com/mankit/v5/home"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/store/internal/disk"
	"go.naywee.com/mankit/v5/store/internal/queue"
)

// files keeps a scope's things as files in its folder, opened once, through the safety core; a change of more than one write is logged ahead in this user's journal and marked in every queue it holds a turn of (contracts mankit/v2 store-journal, store-unfinished).
type files struct {
	s *Scope
	r *os.Root
}

func openFiles(s *Scope) (adapter, error) {
	r, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the folder %s cannot be opened: %v", s.root, err))
	}
	return &files{s: s, r: r}, nil
}

func (f *files) close() error { return f.r.Close() }

// crashAfter, above zero, stops the next change dead after that many writes, leaving the disk as a process killed there would; crashed says it did, and onCrash, when set, ends the process there. Tests only.
var (
	crashAfter atomic.Int64
	crashed    atomic.Bool
	onCrash    func()
	errCrashed = errors.New("store: stopped dead after a write, as a test asked")
)

// beforeReadBack, when set, runs once a change's writes are made and before they are read back, so a test can change the disk there. Tests only.
var beforeReadBack func()

func (f *files) get(key Key) (Item, bool, error) {
	body, err := f.current(key)
	if err != nil || body == nil {
		return Item{}, false, err
	}
	return Item{Body: body, Sum: SumOf(body)}, true, nil
}

// current is what the file at key holds, or nil when there is none; one that cannot be read is an error, never taken for absent (G-CODE-6).
func (f *files) current(key Key) ([]byte, error) {
	body, _, err := disk.Read(f.r, slash(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, diskFail(err, string(key), string(key))
	}
	if body == nil {
		body = []byte{}
	}
	return body, nil
}

// look is what is at key now: a file's body, or a folder; anything else, or a link on the way, is refused.
func (f *files) look(key Key) ([]byte, bool, bool, error) {
	if err := disk.NoLinks(f.r, slash(key)); err != nil {
		return nil, false, false, diskFail(err, string(key), string(key))
	}
	if info, err := f.r.Lstat(slash(key)); err == nil && info.IsDir() {
		return nil, true, true, nil
	}
	body, err := f.current(key)
	return body, body != nil, false, err
}

func (f *files) there(thing Key) bool {
	_, err := f.r.Lstat(slash(thing))
	return err == nil
}

// skip says whether the walk leaves a name out: the store's own folders and the core's temporary files.
func skip(d fs.DirEntry) bool { return d.IsDir() && reserved[d.Name()] || disk.IsTemp(d.Name()) }

func (f *files) list(prefix Key) ([]Key, error) {
	start := "."
	if prefix != "" {
		start = slash(prefix)
		if err := disk.NoLinks(f.r, start); err != nil {
			return nil, diskFail(err, string(prefix), string(prefix))
		}
		info, err := f.r.Lstat(start)
		if errors.Is(err, fs.ErrNotExist) {
			return []Key{}, nil
		}
		if err != nil {
			return nil, diskFail(err, string(prefix), string(prefix))
		}
		if info.Mode().IsRegular() {
			return []Key{prefix}, nil
		}
	}
	out := []Key{}
	err := fs.WalkDir(f.r.FS(), start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != start && skip(d) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, Key(p))
		}
		return nil
	})
	if err != nil {
		return nil, diskFail(err, string(prefix), "the scope cannot be listed")
	}
	return out, nil
}

// revision is a sum over every name in the scope with its kind, size, mode and time, so it moves whenever one is written, made, moved or removed.
func (f *files) revision() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(f.r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && skip(d) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%v\x00%d\x00%d\n", p, info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", diskFail(err, "", "the scope cannot be read")
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (f *files) dirMode() fs.FileMode {
	if f.s.private {
		return 0o700
	}
	return 0o755
}

// modeFor is the mode a file written at key gets: private in the app's own scopes, else the mode of the file it replaces (Q3).
func (f *files) modeFor(key Key) fs.FileMode {
	if f.s.private {
		return 0o600
	}
	if info, err := f.r.Lstat(slash(key)); err == nil && info.Mode().IsRegular() {
		return info.Mode().Perm()
	}
	return 0o644
}

// plan is the lines one op writes, read from the disk as it is now, so an op sees what the ops before it in the change wrote; a Want the disk no longer meets is refused as changed.
func (f *files) plan(o op) ([]line, error) {
	for _, k := range o.keys() {
		if err := disk.NoLinks(f.r, slash(k)); err != nil {
			return nil, diskFail(err, string(k), string(k))
		}
	}
	switch o.kind {
	case opPut:
		info, err := f.r.Lstat(slash(o.key))
		if err == nil && info.IsDir() {
			return nil, problem.FailAt(problem.TypeRefused, ReasonChanged, string(o.key), fmt.Sprintf("%s is a folder, so no file is written there", o.key))
		}
		cur, err := f.current(o.key)
		if err != nil {
			return nil, err
		}
		if err := f.wants(o, cur != nil, false, cur); err != nil {
			return nil, err
		}
		made, err := f.parents(o.key)
		if err != nil {
			return nil, err
		}
		return append(made, line{Key: o.key, Before: content(cur), After: content(o.body), mode: f.modeFor(o.key), exec: o.exec}), nil
	case opRemove:
		info, err := f.r.Lstat(slash(o.key))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, f.wants(o, false, false, nil)
		}
		if err != nil {
			return nil, diskFail(err, string(o.key), string(o.key))
		}
		if info.IsDir() {
			if err := f.wants(o, true, true, nil); err != nil {
				return nil, err
			}
			return f.tree(o.key)
		}
		cur, err := f.current(o.key)
		if err != nil {
			return nil, err
		}
		if err := f.wants(o, true, false, cur); err != nil {
			return nil, err
		}
		return []line{{Key: o.key, Before: content(cur), mode: f.modeFor(o.key)}}, nil
	}
	info, err := f.r.Lstat(slash(o.from))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, problem.FailAt(problem.TypeRefused, ReasonChanged, string(o.from), fmt.Sprintf("%s is not there, so it is not moved", o.from))
	}
	if err != nil {
		return nil, diskFail(err, string(o.from), string(o.from))
	}
	if _, err := f.r.Lstat(slash(o.key)); err == nil {
		return nil, problem.FailAt(problem.TypeRefused, ReasonChanged, string(o.key), fmt.Sprintf("%s is there already, so %s is not moved onto it", o.key, o.from))
	}
	if o.key.within(o.from) {
		return nil, problem.FailAt(problem.TypeRefused, "", string(o.key), fmt.Sprintf("%s is inside %s, so %s cannot move there", o.key, o.from, o.from))
	}
	var cur []byte
	if !info.IsDir() {
		if cur, err = f.current(o.from); err != nil {
			return nil, err
		}
	}
	if err := f.wants(op{key: o.from, want: o.want}, true, info.IsDir(), cur); err != nil {
		return nil, err
	}
	made, err := f.parents(o.key)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return append(made, line{Key: o.key, Dir: true, From: o.from}), nil
	}
	// A file moved is two writes, made then gone, the new one with the old one's mode; a move in one line is kept for folders (contracts store-journal-change).
	mode := f.modeFor(o.from)
	return append(made, line{Key: o.key, After: content(cur), mode: mode}, line{Key: o.from, Before: content(cur), mode: mode}), nil
}

// wants refuses as changed a thing that is not what the op wants.
func (f *files) wants(o op, there, folder bool, body []byte) error {
	return unmet(o.key, o.want, there, folder, body)
}

// parents are the folders above key that are not there yet, outermost first, each a line of its own.
func (f *files) parents(key Key) ([]line, error) {
	var made []line
	for p := path.Dir(string(key)); p != "."; p = path.Dir(p) {
		info, err := f.r.Lstat(p)
		if err == nil {
			if !info.IsDir() {
				return nil, problem.FailAt(problem.TypeRefused, ReasonChanged, p, fmt.Sprintf("%s is a file, so nothing is written inside it", p))
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, diskFail(err, p, p)
		}
		made = append([]line{{Key: Key(p), Dir: true}}, made...)
	}
	return made, nil
}

// tree is the lines removing the folder key writes: each file it holds with its content, then each folder inside it deepest first, then the folder itself, so putting them back gives back what it held.
// A link or anything but a file inside is refused, since its removal could not be put back.
func (f *files) tree(key Key) ([]line, error) {
	var fileLines []line
	var dirs []string
	err := fs.WalkDir(f.r.FS(), slash(key), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if disk.IsTemp(d.Name()) && !d.IsDir() {
			return nil
		}
		// A name no key can hold is refused before anything is written, so every line a journal holds is a key.
		if err := Key(p).check(); err != nil {
			return problem.FailAt(problem.TypeRefused, ReasonOutside, "", fmt.Sprintf("%s holds a name no key can hold, so it is not removed: %v", key, err))
		}
		switch {
		case d.IsDir():
			if reserved[d.Name()] {
				return problem.FailAt(problem.TypeRefused, ReasonOutside, p, fmt.Sprintf("%s holds %s, the store's own folder, so it is not removed", key, p))
			}
			if Key(p) != key {
				dirs = append(dirs, p)
			}
		case d.Type().IsRegular():
			body, err := f.current(Key(p))
			if err != nil {
				return err
			}
			fileLines = append(fileLines, line{Key: Key(p), Before: content(body), mode: f.modeFor(Key(p))})
		default:
			return problem.FailAt(problem.TypeRefused, ReasonOutside, p, fmt.Sprintf("%s holds %s, which is not a file or a folder, so it is not removed", key, p))
		}
		return nil
	})
	if err != nil {
		return nil, diskFail(err, string(key), string(key))
	}
	sort.SliceStable(dirs, func(i, j int) bool { return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/") })
	out := fileLines
	for _, d := range dirs {
		out = append(out, line{Key: Key(d), Dir: true, Gone: true})
	}
	return append(out, line{Key: key, Dir: true, Gone: true}), nil
}

// apply makes one line's write, and says the folders it changed; synced, it also syncs them.
func (f *files) apply(l line, synced bool) ([]string, error) {
	name := slash(l.Key)
	dirs := []string{path.Dir(name)}
	var err error
	switch {
	case l.From != "":
		err = f.r.Rename(slash(l.From), name)
		dirs = append(dirs, path.Dir(slash(l.From)))
	case l.Dir && l.Gone:
		// A temporary file the core left there is no thing, and would keep the folder from going.
		if err = f.sweep(name, ""); err == nil {
			err = f.r.Remove(name)
		}
	case l.Dir:
		err = f.r.Mkdir(name, f.dirMode())
	case l.After != nil:
		err = disk.WriteContent(f.r, name, *l.After, f.fileMode(l))
	default:
		err = f.r.Remove(name)
	}
	if err != nil {
		return nil, err
	}
	if synced {
		return dirs, f.syncAll(dirs)
	}
	return dirs, nil
}

// fileMode is the mode a line's file is written with: the one it recorded, or as modeFor says.
func (f *files) fileMode(l line) fs.FileMode {
	m := f.modeFor(l.Key)
	if l.mode != 0 && !f.s.private {
		m = l.mode
	}
	if l.exec {
		// The run bit wherever the file may be read: 0600 becomes 0700, 0644 becomes 0755.
		m |= (m & 0o444) >> 2
	}
	return m
}

// syncAll syncs each folder once; one that is gone since has nothing to sync.
func (f *files) syncAll(dirs []string) error {
	var done []string
	for _, d := range dirs {
		if slices.Contains(done, d) {
			continue
		}
		done = append(done, d)
		if err := disk.SyncDir(f.r, d); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// sweep removes the core's temporary files in the folder dir, those of the file base only when base is given; a crash can leave one beside a file it was writing.
func (f *files) sweep(dir, base string) error {
	d, err := f.r.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		if disk.IsTemp(n) && (base == "" || strings.HasPrefix(n, "."+base+disk.TempMark)) {
			if err := f.r.Remove(path.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// commit makes ops all or nothing: one write alone is whole by itself; more are marked in every queue held, logged ahead in this user's journal, made, read back, and the journal removed before the marks (contracts store-journal).
func (f *files) commit(t *turns, ops []op) error {
	n := 0
	step := func() error {
		n++
		if c := crashAfter.Load(); c > 0 && int64(n) == c {
			crashAfter.Store(0)
			crashed.Store(true)
			if onCrash != nil {
				onCrash()
			}
			return errCrashed
		}
		return nil
	}
	over := func() error {
		if t.over() {
			return errors.New("the turn's limit passed, so the change stopped before its next write")
		}
		return nil
	}
	first, err := f.plan(ops[0])
	if err != nil {
		return err
	}
	if len(ops) == 1 && len(first) <= 1 {
		if len(first) == 0 {
			return nil
		}
		if err := over(); err != nil {
			return err
		}
		if _, err := f.apply(first[0], true); err != nil {
			return diskFail(err, string(first[0].Key), string(first[0].Key))
		}
		if err := step(); err != nil {
			return err
		}
		if beforeReadBack != nil {
			beforeReadBack()
		}
		return f.readBack(first)
	}
	id := newID()
	var keys []Key
	for _, o := range ops {
		for _, k := range o.keys() {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	m := newMark(id, id, t.holds(""), keys)
	marks := t.dirs()
	for i, dir := range marks {
		if err := writeMark(dir, m); err != nil {
			removeMarks(marks[:i], id)
			return err
		}
		if err := step(); err != nil {
			return err
		}
	}
	logged := 0
	encode := func(lines []line) ([]byte, error) {
		var text []byte
		for _, l := range lines {
			logged++
			b, err := encodeLine(logged, l)
			if err != nil {
				return nil, err
			}
			text = append(text, b...)
		}
		return text, nil
	}
	text, err := encode(first)
	if err != nil {
		removeMarks(marks, id)
		return err
	}
	jr, name, err := f.startJournal(id, text)
	if err != nil {
		removeMarks(marks, id)
		return err
	}
	defer jr.Close()
	journal := filepath.Join(jr.Name(), name)
	var done []line
	undo := func(cause error) error {
		if errors.Is(cause, errCrashed) {
			return cause
		}
		if left, err := f.undo(done); left > 0 || err != nil {
			return errors.Join(cause, f.stuck(done[:left], err, journal))
		}
		if err := jr.Remove(name); err != nil {
			return errors.Join(cause, problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the change was put back, but its journal %s could not be removed: %v", journal, err)))
		}
		f.syncJournals(jr, journal)
		removeMarks(marks, id)
		return cause
	}
	if err := step(); err != nil {
		return err
	}
	var dirs []string
	for i, o := range ops {
		lines := first
		if i > 0 {
			if err := over(); err != nil {
				return undo(err)
			}
			if lines, err = f.plan(o); err != nil {
				return undo(err)
			}
			text, err := encode(lines)
			if err != nil {
				return undo(err)
			}
			if err := disk.Append(jr, name, text); err != nil {
				return undo(problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the journal %s cannot be written: %v", journal, err)))
			}
			if err := step(); err != nil {
				return err
			}
		}
		for _, l := range lines {
			if err := over(); err != nil {
				return undo(err)
			}
			changed, err := f.apply(l, false)
			if err != nil {
				return undo(diskFail(err, string(l.Key), string(l.Key)))
			}
			done = append(done, l)
			dirs = append(dirs, changed...)
			if err := step(); err != nil {
				return err
			}
		}
	}
	// Each folder written in is synced once, before the journal that could put it back goes.
	if err := f.syncAll(dirs); err != nil {
		return undo(diskFail(err, "", "a folder the change wrote in"))
	}
	if beforeReadBack != nil {
		beforeReadBack()
	}
	if err := f.readBack(done); err != nil {
		return undo(err)
	}
	if err := jr.Remove(name); err != nil {
		return problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the change was made, but its journal %s could not be removed, so the next run puts it back: %v", journal, err))
	}
	f.syncJournals(jr, journal)
	if err := step(); err != nil {
		return err
	}
	for _, dir := range marks {
		if err := os.Remove(markPath(dir, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the change was made, but its mark %s could not be removed: %v", markPath(dir, id), err))
		}
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// stuck is a change that could not all be put back: its journal and marks stay, so nothing else changes the thing until it is.
func (f *files) stuck(left []line, err error, journal string) error {
	if err != nil {
		return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("putting the change back stopped: %v; what was there before is kept in %s", err, journal))
	}
	last := left[len(left)-1].Key
	return problem.FailAt(problem.TypeFailed, ReasonUnfinished, string(last), fmt.Sprintf("putting the change back stopped at %s, which was changed since, so it and what came before it were left as they are; what was there before is kept in %s", last, journal))
}

// readBack checks the disk holds what the lines left, each key where and as the last line touching it leaves it: a folder moved takes what was written inside it along, and what was there under its new name before is gone.
func (f *files) readBack(lines []line) error {
	type end struct {
		there, dir bool
		body       []byte
	}
	ends := map[Key]end{}
	var order []Key
	listed := map[Key]bool{}
	set := func(k Key, e end) {
		if !listed[k] {
			listed[k] = true
			order = append(order, k)
		}
		ends[k] = e
	}
	for _, l := range lines {
		switch {
		case l.From != "":
			moved := map[Key]end{}
			for k, e := range ends {
				switch {
				case k.within(l.Key):
					delete(ends, k)
				case k.within(l.From):
					delete(ends, k)
					moved[k.rebase(l.From, l.Key)] = e
				}
			}
			set(l.From, end{})
			set(l.Key, end{there: true, dir: true})
			for k, e := range moved {
				set(k, e)
			}
		case l.Dir:
			set(l.Key, end{there: !l.Gone, dir: true})
		case l.After != nil:
			set(l.Key, end{there: true, body: *l.After})
		default:
			set(l.Key, end{})
		}
	}
	for _, k := range order {
		e, kept := ends[k]
		if !kept {
			// Moved away with its folder, or under a name a folder was moved onto.
			continue
		}
		info, err := f.r.Lstat(slash(k))
		ok := false
		switch {
		case !e.there:
			ok = errors.Is(err, fs.ErrNotExist)
		case e.dir:
			ok = err == nil && info.IsDir()
		default:
			body, rerr := f.current(k)
			ok = rerr == nil && body != nil && bytes.Equal(body, e.body)
		}
		if !ok {
			return problem.FailAt(problem.TypeFailed, queue.ReasonNotReadBack, string(k), fmt.Sprintf("%s was written but could not be read back as written", k))
		}
	}
	return nil
}

// undo puts lines back, last first, and says how many are left: it stops at a file that holds neither what was written nor what was there, since somebody changed it since. Every folder it touched is synced before it returns, so the journal may go.
func (f *files) undo(lines []line) (int, error) {
	var dirs []string
	left := 0
	var err error
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		dirs = append(dirs, path.Dir(slash(l.Key)))
		if l.From != "" {
			dirs = append(dirs, path.Dir(slash(l.From)))
		}
		var done bool
		if done, err = f.undoOne(l); err != nil || !done {
			left = i + 1
			break
		}
	}
	if serr := f.syncAll(dirs); err == nil && serr != nil {
		return left, serr
	}
	return left, err
}

// undoOne puts one line back; done is false where it is not the store's to put back any more.
func (f *files) undoOne(l line) (bool, error) {
	name := slash(l.Key)
	if err := disk.NoLinks(f.r, name); err != nil {
		return false, diskFail(err, string(l.Key), string(l.Key))
	}
	_, err := f.r.Lstat(name)
	there := err == nil
	switch {
	case l.From != "":
		_, ferr := f.r.Lstat(slash(l.From))
		from := ferr == nil
		switch {
		case there && !from:
			if err := f.makeDirs(path.Dir(slash(l.From))); err != nil {
				return false, err
			}
			return true, f.r.Rename(name, slash(l.From))
		case there && from:
			return false, nil
		}
		return true, nil
	case l.Dir && l.Gone:
		if there {
			return true, nil
		}
		return true, f.r.Mkdir(name, f.dirMode())
	case l.Dir:
		if !there {
			return true, nil
		}
		if err := f.sweep(name, ""); err != nil {
			return false, err
		}
		// A folder somebody has put a file in is theirs now.
		return f.r.Remove(name) == nil, nil
	}
	// A temporary file a crash left beside the file is the core's, never somebody's change.
	if err := f.sweep(path.Dir(name), path.Base(name)); err != nil {
		return false, err
	}
	now, err := f.current(l.Key)
	if err != nil {
		return false, err
	}
	before, after := deref(l.Before), deref(l.After)
	switch {
	case same(now, before):
		return true, nil
	case !same(now, after):
		return false, nil
	case before == nil:
		return true, f.r.Remove(name)
	}
	if err := f.makeDirs(path.Dir(name)); err != nil {
		return false, err
	}
	return true, disk.WriteContent(f.r, name, before, f.fileMode(l))
}

// makeDirs makes the folder dir and those above it that are gone, syncing the folder each was made in.
func (f *files) makeDirs(dir string) error {
	var made []string
	for p := dir; p != "."; p = path.Dir(p) {
		if _, err := f.r.Lstat(p); err == nil {
			break
		}
		made = append(made, p)
	}
	if err := f.r.MkdirAll(dir, f.dirMode()); err != nil {
		return err
	}
	for _, p := range made {
		if err := disk.SyncDir(f.r, path.Dir(p)); err != nil {
			return err
		}
	}
	return nil
}

func deref(b *[]byte) []byte {
	if b == nil {
		return nil
	}
	if *b == nil {
		return []byte{}
	}
	return *b
}

// same compares two contents, where nil is no file at all and differs from an empty one.
func same(a, b []byte) bool { return (a == nil) == (b == nil) && bytes.Equal(a, b) }

// journals is this user's folder of journals on this machine, private: one per machine, since a lock may not hold across a bind mount, so a host and its container keep theirs apart.
func (f *files) journals() (string, error) {
	data, err := home.ProductData(f.s.app.ID)
	if err != nil {
		return "", problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("%s's data folder cannot be found: %v", f.s.app.ID, err))
	}
	sum := sha256.Sum256([]byte(hostname()))
	dir := filepath.Join(data, ".store", hex.EncodeToString(sum[:4]))
	tightened, err := disk.Private(dir)
	if err != nil {
		return "", diskFail(err, "", "the journal folder "+dir)
	}
	for _, p := range tightened {
		fmt.Fprintf(f.log(), "store: %s was open to other users; it is now this user's alone\n", p)
	}
	return dir, nil
}

// startJournal writes a journal's first line whole with the lines of the change's first write, so a journal there always says whose it is.
func (f *files) startJournal(id string, lines []byte) (*os.Root, string, error) {
	dir, err := f.journals()
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", diskFail(err, "", "the journal folder "+dir)
	}
	name := id + journalExt
	head, err := encodeHead(id, f.s.app.ID, f.s.root)
	if err == nil {
		err = disk.Write(r, name, append(head, lines...), 0o600)
	}
	if err != nil {
		r.Close()
		return nil, "", problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the journal %s cannot be written: %v", filepath.Join(dir, name), err))
	}
	return r, name, nil
}

func markPath(dir, id string) string { return filepath.Join(dir, markPrefix+id+markSuffix) }

// writeMark writes the change's mark whole in the queue folder dir.
func writeMark(dir string, m mark) error {
	data, err := json.MarshalIndent(m, "", "  ")
	data = append(data, '\n')
	if err == nil {
		var r *os.Root
		if r, err = os.OpenRoot(dir); err == nil {
			err = disk.Write(r, filepath.Base(markPath(dir, m.Metadata.ID)), data, 0o600)
			r.Close()
		}
	}
	if err != nil {
		return problem.FailAt(problem.TypeFailed, queue.ReasonUnusable, "", fmt.Sprintf("the change's mark cannot be written in %s: %v", dir, err))
	}
	return nil
}

// removeMarks takes a change's marks away when it never began or was put back.
func removeMarks(dirs []string, id string) {
	for _, dir := range dirs {
		_ = os.Remove(markPath(dir, id))
	}
}

// marksIn are the marks in the queue folder dir; a folder or a mark that cannot be read is said, never taken for none (G-CODE-6).
func marksIn(dir string) ([]mark, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, problem.FailAt(problem.TypeFailed, queue.ReasonUnusable, "", fmt.Sprintf("the change queue %s cannot be read: %v", dir, err))
	}
	var out []mark
	for _, e := range entries {
		got := markName.FindStringSubmatch(e.Name())
		if got == nil {
			continue
		}
		m, err := readMarkFile(dir, e.Name(), got[1])
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func readMarkFile(dir, name, id string) (mark, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return mark{}, problem.FailAt(problem.TypeFailed, queue.ReasonUnusable, "", fmt.Sprintf("the change queue %s cannot be read: %v", dir, err))
	}
	defer r.Close()
	data, _, err := disk.Read(r, name)
	if err != nil {
		return mark{}, problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the mark of a change cut short, %s, cannot be read, so nothing it may hold back is changed: %v", filepath.Join(dir, name), err))
	}
	m, err := readMark(data, id)
	if err != nil {
		return mark{}, problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the mark of a change cut short, %s, %v, so nothing it may hold back is changed", filepath.Join(dir, name), err))
	}
	return m, nil
}

// mine says whether a mark is this user's on this machine, whose journal this run can put back.
func mine(m mark) bool { return m.Spec.User == userName() && m.Spec.Host == hostname() }

// markLines are the lines a mark has a copy in: the folder's own when it held that turn, and each of its keys' things.
func (f *files) markLines(m mark) []string {
	var out []string
	add := func(line string) {
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	if m.Spec.Folder {
		add(f.s.line(""))
	}
	for _, k := range m.Spec.Keys {
		add(f.s.line(k.thing()))
	}
	return out
}

func (f *files) blocked(t *turns, key Key) error {
	for _, dir := range t.dirs() {
		marks, err := marksIn(dir)
		if err != nil {
			return err
		}
		for _, m := range marks {
			if key == "" && m.Spec.Folder {
				return f.refuse(m, "the folder "+f.s.root)
			}
			for _, k := range m.Spec.Keys {
				if key != "" && overlaps(key, k) {
					return f.refuse(m, string(key))
				}
			}
		}
	}
	return nil
}

// refuse is unfinished_change, naming who must run again, where, and the command that clears the mark (contracts store-unfinished).
func (f *files) refuse(m mark, what string) error {
	app, thing := f.s.app.command(), m.Spec.Keys[0].thing()
	p := problem.FailAt(problem.TypeRefused, ReasonUnfinished, string(m.Spec.Keys[0]), fmt.Sprintf(
		"%s is not changed: a change %s made on %s was cut short and waits to be put back; %s's next run of %s on %s puts it back, and once sure it was put back or given up, `%s store unfinished remove %s` clears it",
		what, m.Spec.User, m.Spec.Host, m.Spec.User, app, m.Spec.Host, app, thing))
	return p
}

// settle puts back this user's changes cut short whose marks are in the lines a change to thing meets: the folder's own and the thing's, or for the scope itself every thing's.
func (f *files) settle(ctx context.Context, thing Key) error {
	lines := []string{f.s.line("")}
	things := []Key{thing}
	if thing == "" {
		things = nil
		entries, err := os.ReadDir(f.s.root)
		if err != nil {
			return problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the folder %s cannot be read: %v", f.s.root, err))
		}
		for _, e := range entries {
			if !reserved[e.Name()] && !disk.IsTemp(e.Name()) {
				things = append(things, Key(e.Name()))
			}
		}
	}
	for _, th := range things {
		if l := f.s.line(th); !slices.Contains(lines, l) {
			lines = append(lines, l)
		}
	}
	seen := map[string]bool{}
	for _, l := range lines {
		marks, err := marksIn(f.s.queueFor(l).Dir())
		if err != nil {
			return err
		}
		for _, m := range marks {
			if !mine(m) || seen[m.Metadata.ID] {
				continue
			}
			seen[m.Metadata.ID] = true
			if err := f.recoverMark(ctx, m); err != nil {
				// A change cut short that this one does not meet waits for a change that does; this one goes on.
				if !f.meets(m, thing) {
					fmt.Fprintf(f.log(), "store: %v\n", err)
					continue
				}
				return err
			}
		}
	}
	return nil
}

// meets says whether a change to thing meets what mark m holds: a key of the thing, or the folder's own turn, which a change to the scope itself or to a thing not there yet takes.
func (f *files) meets(m mark, thing Key) bool {
	if m.Spec.Folder && (thing == "" || !f.there(thing)) {
		return true
	}
	return thing != "" && slices.ContainsFunc(m.Spec.Keys, func(k Key) bool { return k.thing() == thing })
}

// recoverMark puts back the change of one of this user's marks in its turns, the folder's first, then removes its journal and, last, its marks.
func (f *files) recoverMark(ctx context.Context, m mark) error {
	t := f.s.turns(ctx, "put back a change cut short")
	err := func() error {
		if err := t.take(""); err != nil {
			return err
		}
		for _, k := range m.Spec.Keys {
			if err := t.take(k.thing()); err != nil {
				return err
			}
		}
		if !f.markThere(m) {
			return nil
		}
		return f.putBack(m)
	}()
	return t.give(err)
}

// markThere says whether any copy of a mark is still there; another run of this user's may have put it back meanwhile.
func (f *files) markThere(m mark) bool {
	for _, l := range f.markLines(m) {
		if _, err := os.Lstat(markPath(f.s.queueFor(l).Dir(), m.Metadata.ID)); err == nil {
			return true
		}
	}
	return false
}

// putBack undoes the journal a mark names, if it is there; a mark with no journal is a change that finished but for its marks.
func (f *files) putBack(m mark) error {
	dir, err := f.journals()
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return diskFail(err, "", "the journal folder "+dir)
	}
	defer r.Close()
	name := m.Spec.Journal + journalExt
	journal := filepath.Join(dir, name)
	raw, info, err := disk.Read(r, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f.dropMarks(m)
	case errors.Is(err, disk.ErrLink):
		return problem.FailAt(problem.TypeFailed, ReasonNotOwned, "", fmt.Sprintf("the journal %s is a link, so it is not put back and nothing it holds back is changed", journal))
	case err != nil:
		return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the journal %s cannot be read, so nothing is put back and nothing it holds back is changed: %v", journal, err))
	}
	if err := disk.OwnFile(info, journal); err != nil {
		return problem.FailAt(problem.TypeFailed, ReasonNotOwned, "", fmt.Sprintf("the journal %s is not this user's own, or others can write it, so it is not put back", journal))
	}
	head, lines, err := parseJournal(raw)
	if err != nil {
		return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the journal %s %v, so nothing is put back and nothing it holds back is changed", journal, err))
	}
	if head.Metadata.Host == hostname() && head.Metadata.PID != os.Getpid() && alive(head.Metadata.PID) {
		return problem.FailAt(problem.TypeRefused, ReasonUnfinished, string(m.Spec.Keys[0]), fmt.Sprintf("the journal %s belongs to process %d, which still runs on this machine, so it is not put back; wait for it to end", journal, head.Metadata.PID))
	}
	if head.Metadata.ID != m.Spec.Journal || head.Spec.Mark != m.Metadata.ID {
		return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the journal %s is %s with the mark %s, not the journal the mark %s names, so it is not put back", journal, head.Metadata.ID, head.Spec.Mark, m.Metadata.ID))
	}
	if !sameFolder(head.Spec.Scope, f.s.root) {
		return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the journal %s names the folder %s, not %s, so it is not put back here", journal, head.Spec.Scope, f.s.root))
	}
	left, err := f.undo(lines)
	if left > 0 || err != nil {
		var rest []line
		if left > 0 {
			rest = lines[:left]
		}
		if err == nil && len(rest) == 0 {
			err = errors.New("nothing was left, yet it stopped")
		}
		return f.stuck(rest, err, journal)
	}
	fmt.Fprintf(f.log(), "store: a change %s cut short in %s was put back (journal %s)\n", head.Metadata.App, f.s.root, journal)
	if err := r.Remove(name); err != nil {
		return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the change in %s was put back, but its journal could not be removed: %v", journal, err))
	}
	f.syncJournals(r, journal)
	return f.dropMarks(m)
}

// sameFolder says whether two paths name one folder, links and all.
func sameFolder(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

// syncJournals syncs the journal folder once a journal is gone; the change is made either way, so a sync that fails is said, not returned.
func (f *files) syncJournals(r *os.Root, journal string) {
	if err := disk.SyncDir(r, "."); err != nil {
		fmt.Fprintf(f.log(), "store: the folder of %s, removed, could not be synced: %v\n", journal, err)
	}
}

// dropMarks removes every copy of a mark, the last thing a change does.
func (f *files) dropMarks(m mark) error {
	for _, l := range f.markLines(m) {
		p := markPath(f.s.queueFor(l).Dir(), m.Metadata.ID)
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return problem.FailAt(problem.TypeFailed, ReasonUnfinished, "", fmt.Sprintf("the mark %s cannot be removed: %v", p, err))
		}
	}
	return nil
}

func (f *files) log() io.Writer {
	if f.s.app.Log != nil {
		return f.s.app.Log
	}
	return os.Stderr
}

func (f *files) unfinished(thing Key) ([]Unfinished, error) {
	marks, err := marksIn(f.s.queueFor(f.s.line(thing)).Dir())
	if err != nil {
		return nil, err
	}
	out := []Unfinished{}
	for _, m := range marks {
		if thing == "" || slices.ContainsFunc(m.Spec.Keys, func(k Key) bool { return k.thing() == thing }) {
			out = append(out, f.withFile(m.unfinished()))
		}
	}
	left, err := f.leftJournals(thing)
	return append(out, left...), err
}

// withFile names u's journal when it is this user's on this machine and still there.
func (f *files) withFile(u Unfinished) Unfinished {
	if u.User != userName() || u.Host != hostname() {
		return u
	}
	if dir, err := f.journals(); err == nil {
		if p := filepath.Join(dir, u.Journal+journalExt); fileThere(p) {
			u.File = p
		}
	}
	return u
}

func fileThere(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// leftJournals are this user's journals of this scope whose marks are gone, with the keys they name inside thing, or all when thing is empty.
func (f *files) leftJournals(thing Key) ([]Unfinished, error) {
	dir, err := f.journals()
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, diskFail(err, "", "the journal folder "+dir)
	}
	defer r.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, diskFail(err, "", "the journal folder "+dir)
	}
	var out []Unfinished
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), journalExt) {
			continue
		}
		// A journal that cannot be read may be any scope's, so it is said and passed over, never taken for none and never failing every scope's list.
		raw, _, err := disk.Read(r, e.Name())
		if err != nil {
			fmt.Fprintf(f.log(), "store: the journal %s cannot be read, so it is not listed: %v\n", filepath.Join(dir, e.Name()), err)
			continue
		}
		head, lines, err := parseJournal(raw)
		if err != nil {
			fmt.Fprintf(f.log(), "store: the journal %s %v, so it is not listed\n", filepath.Join(dir, e.Name()), err)
			continue
		}
		if !sameFolder(head.Spec.Scope, f.s.root) {
			continue
		}
		var m mark
		m.Metadata.ID, m.Spec.Folder = head.Spec.Mark, true
		for _, l := range lines {
			for _, k := range []Key{l.Key, l.From} {
				if k != "" && !slices.Contains(m.Spec.Keys, k) {
					m.Spec.Keys = append(m.Spec.Keys, k)
				}
			}
		}
		if f.markThere(m) || thing != "" && !slices.ContainsFunc(m.Spec.Keys, func(k Key) bool { return k.thing() == thing }) {
			continue
		}
		out = append(out, shown(Unfinished{ID: head.Spec.Mark, User: userName(), Host: head.Metadata.Host, Journal: head.Metadata.ID, Keys: m.Spec.Keys, File: filepath.Join(dir, e.Name()), Left: true}))
	}
	return out, nil
}

func (f *files) clear(ctx context.Context, thing Key, ids []string) ([]Unfinished, error) {
	marks, err := marksIn(f.s.queueFor(f.s.line(thing)).Dir())
	if err != nil {
		return nil, err
	}
	out := []Unfinished{}
	for _, m := range marks {
		if thing != "" && !slices.ContainsFunc(m.Spec.Keys, func(k Key) bool { return k.thing() == thing }) || !slices.Contains(ids, m.Metadata.ID) {
			continue
		}
		t := f.s.turns(ctx, "clear a change cut short")
		dropped := false
		err := func() error {
			if err := t.take(""); err != nil {
				return err
			}
			for _, k := range m.Spec.Keys {
				if err := t.take(k.thing()); err != nil {
					return err
				}
			}
			// Read before the turns were taken: a mark its user's run put back, or another person cleared, meanwhile is gone and not counted.
			if !f.markThere(m) {
				return nil
			}
			dropped = true
			return f.dropMarks(m)
		}()
		if err := t.give(err); err != nil {
			return out, err
		}
		if dropped {
			out = append(out, f.withFile(m.unfinished()))
		}
	}
	return out, nil
}
