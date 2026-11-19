// Package storewall is the storage wall (ADR-S22): an app writes no file outside mankit's store, and encodes YAML or JSON only in the store and in helpout.
// It fails a call that writes, removes, moves, makes, locks or changes the mode of a file, or that encodes YAML or JSON, outside those homes, unless the app's .storewall names the package and why.
//
// .storewall sits beside go.mod; each line is one exception, # starts a comment:
//
//	write   internal/install   installs the app's binary where the user asked
//	encode  export             writes the design the user asked to export
//	write   ...:tests          tests make their fixtures in t.TempDir()
//
// The first word is write or encode, what the package may do. The second is the package, as a path inside the module:
// . is the module's own, a/... is a and everything below it, ... is every package; :tests limits it to the _test.go files.
// The rest of the line is why, and a line without a why is refused.
package storewall

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the wall, for go vet's -vettool or a singlechecker.
var Analyzer = &analysis.Analyzer{
	Name:     "storewall",
	Doc:      "fails a file written, or YAML or JSON encoded, outside mankit's store (ADR-S22)",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

// FileName is the app's list of exceptions, beside its go.mod.
const FileName = ".storewall"

// The two things the wall forbids, each the first word of an exception.
const (
	write  = "write"
	encode = "encode"
)

type marker struct{}

// mankit is this module's path, read from the package itself so a new major (/vN) needs no edit here.
var mankit = strings.TrimSuffix(reflect.TypeOf(marker{}).PkgPath(), "/internal/storewall")

// homes are the packages that may do each thing: the store and its safety core write and encode what they keep; helpout encodes what -o prints.
func homes() map[string][]string {
	core := []string{mankit + "/store", mankit + "/internal/disk"}
	return map[string][]string{
		write:  core,
		encode: append(core, mankit+"/helpout"),
	}
}

// Calls that write a file. A name is "pkg.Func" or "(pkg.Type).Method"; the number is which argument holds the open flags, for a call that only writes with a write flag.
var writes = map[string]int{}

// Calls that encode YAML or JSON.
var encodes = map[string]bool{}

func init() {
	for _, f := range []string{"WriteFile", "Create", "CreateTemp", "MkdirTemp", "Mkdir", "MkdirAll", "Remove", "RemoveAll", "Rename", "Chmod", "Chown", "Lchown", "Chtimes", "Link", "Symlink", "Truncate", "CopyFS"} {
		writes["os."+f] = -1
	}
	writes["os.OpenFile"] = 1
	for _, f := range []string{"Chmod", "Chown", "Truncate"} {
		writes["(os.File)."+f] = -1
	}
	for _, f := range []string{"WriteFile", "Create", "Mkdir", "MkdirAll", "Remove", "RemoveAll", "Rename", "Chmod", "Chown", "Lchown", "Chtimes", "Link", "Symlink"} {
		writes["(os.Root)."+f] = -1
	}
	writes["(os.Root).OpenFile"] = 1
	for _, f := range []string{"WriteFile", "TempFile", "TempDir"} {
		writes["io/ioutil."+f] = -1
	}
	for _, pkg := range []string{"syscall", "golang.org/x/sys/unix"} {
		for _, f := range []string{"Flock", "Creat", "Rename", "Renameat", "Renameat2", "RenameatxNp", "Unlink", "Unlinkat", "Rmdir", "Mkdir", "Mkdirat", "Mkfifo", "Mknod", "Chmod", "Fchmod", "Fchmodat", "Chown", "Fchown", "Lchown", "Link", "Linkat", "Symlink", "Symlinkat", "Truncate", "Ftruncate"} {
			writes[pkg+"."+f] = -1
		}
		writes[pkg+".Open"] = 1
		writes[pkg+".Openat"] = 2
	}
	writes["github.com/gofrs/flock.New"] = -1
	for _, f := range []string{"encoding/json.Marshal", "encoding/json.MarshalIndent", "encoding/json.NewEncoder",
		"gopkg.in/yaml.v3.Marshal", "gopkg.in/yaml.v3.NewEncoder", "gopkg.in/yaml.v2.Marshal", "gopkg.in/yaml.v2.NewEncoder",
		"sigs.k8s.io/yaml.Marshal", "sigs.k8s.io/yaml.JSONToYAML"} {
		encodes[f] = true
	}
}

// writeFlags are the open flags that write: any access but read-only, or making, emptying or adding to the file.
const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_APPEND

func run(pass *analysis.Pass) (any, error) {
	if len(pass.Files) == 0 {
		return nil, nil
	}
	ex, err := exceptionsFor(pass.Fset.File(pass.Files[0].Pos()).Name())
	if err != nil {
		return nil, err
	}
	pkg := strings.TrimSuffix(pass.Pkg.Path(), "_test")
	allowed := func(what, file string) bool {
		for _, h := range homes()[what] {
			if pkg == h {
				return true
			}
		}
		return ex.allow(what, pkg, strings.HasSuffix(file, "_test.go"))
	}
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.WithStack([]ast.Node{(*ast.Ident)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
		if !push {
			return true
		}
		id := n.(*ast.Ident)
		fn, ok := pass.TypesInfo.Uses[id].(*types.Func)
		if !ok || fn.Pkg() == nil {
			return true
		}
		name := nameOf(fn)
		file := pass.Fset.File(id.Pos()).Name()
		if flagArg, ok := writes[name]; ok && !allowed(write, file) {
			if flagArg >= 0 && readOnly(pass, call(id, stack), flagArg) {
				return true
			}
			pass.Reportf(id.Pos(), "%s writes a file outside mankit's store: keep it through the store (ADR-S22), or name this package and why in %s", short(name), FileName)
		}
		if encodes[name] && !allowed(encode, file) {
			pass.Reportf(id.Pos(), "%s encodes outside the store and helpout: keep it through the store, print it with helpout.Encode (ADR-S22), or name this package and why in %s", short(name), FileName)
		}
		return true
	})
	return nil, nil
}

// nameOf is a function's name as the lists hold it: pkg.Func, or (pkg.Type).Method for a method, pointer or not.
func nameOf(fn *types.Func) string {
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return fn.Pkg().Path() + "." + fn.Name()
	}
	t := sig.Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil {
		return "(" + named.Obj().Pkg().Path() + "." + named.Obj().Name() + ")." + fn.Name()
	}
	return fn.Name()
}

// short is a name with its package's last step only, as a person writes it: os.WriteFile, yaml.Marshal.
func short(name string) string {
	open := strings.HasPrefix(name, "(")
	name = strings.TrimPrefix(name, "(")
	if i := strings.LastIndex(name[:strings.LastIndex(name, ".")], "/"); i >= 0 {
		name = name[i+1:]
	}
	if open {
		name = "(" + name
	}
	return name
}

// call is the call id names the function of, or nil when id is used as a value.
func call(id *ast.Ident, stack []ast.Node) *ast.CallExpr {
	var fun ast.Node = id
	for i := len(stack) - 2; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.SelectorExpr:
			if p.Sel != id {
				return nil
			}
			fun = p
		case *ast.ParenExpr:
			fun = p
		case *ast.CallExpr:
			if p.Fun == fun {
				return p
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// readOnly says whether c opens with flags that are known and never write; a flag the wall cannot read is taken as a write.
func readOnly(pass *analysis.Pass, c *ast.CallExpr, arg int) bool {
	if c == nil || arg >= len(c.Args) {
		return false
	}
	tv, ok := pass.TypesInfo.Types[c.Args[arg]]
	if !ok || tv.Value == nil {
		return false
	}
	v, ok := constant.Int64Val(constant.ToInt(tv.Value))
	return ok && v&int64(writeFlags) == 0
}

// exception is one line of .storewall.
type exception struct {
	what, where string
	tests       bool
}

type exceptions struct {
	module string
	list   []exception
}

// allow says whether the package pkg may do what, in a test file when test.
func (e exceptions) allow(what, pkg string, test bool) bool {
	for _, x := range e.list {
		if x.what != what || x.tests && !test {
			continue
		}
		if within(pkg, e.module, x.where) {
			return true
		}
	}
	return false
}

// within says whether pkg is the package where names inside module.
func within(pkg, module, where string) bool {
	full := module
	if base, all := strings.CutSuffix(where, "..."); all {
		base = strings.TrimSuffix(base, "/")
		if base != "" {
			full = module + "/" + base
		}
		return pkg == full || strings.HasPrefix(pkg, full+"/")
	}
	if where != "." {
		full = module + "/" + where
	}
	return pkg == full
}

var (
	readMu sync.Mutex
	read   = map[string]exceptions{}
)

// exceptionsFor reads the .storewall of the module the file is in, once per module.
func exceptionsFor(file string) (exceptions, error) {
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		up := filepath.Dir(dir)
		if up == dir {
			return exceptions{}, nil // no module: no exceptions
		}
		dir = up
	}
	readMu.Lock()
	defer readMu.Unlock()
	if e, ok := read[dir]; ok {
		return e, nil
	}
	e, err := load(dir)
	if err != nil {
		return exceptions{}, err
	}
	read[dir] = e
	return e, nil
}

// load is the module in dir and its .storewall; a missing file is no exceptions, a line it cannot read is an error naming it.
func load(dir string) (exceptions, error) {
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return exceptions{}, err
	}
	var e exceptions
	for _, line := range strings.Split(string(mod), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			e.module = strings.Trim(strings.TrimSpace(m), `"`)
			break
		}
	}
	path := filepath.Join(dir, FileName)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return exceptions{}, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 3 || f[0] != write && f[0] != encode {
			return exceptions{}, fmt.Errorf("%s:%d: want write or encode, a package and why it may: %q", path, n, sc.Text())
		}
		x := exception{what: f[0], where: f[1]}
		x.where, x.tests = strings.CutSuffix(x.where, ":tests")
		e.list = append(e.list, x)
	}
	return e, sc.Err()
}
