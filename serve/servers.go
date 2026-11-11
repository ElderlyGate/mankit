package serve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tugbayatilla/mankit/home"
)

// A record is a claim a detached server leaves, never proof: a process can die without removing it, and a pid is reused.
// So a record is believed only once its address answers, and one whose address refuses a connection is removed as it is read.

// The record's fixed header: two app versions read it at once, so these never change (ADR-S15, contracts mankit/v1).
const (
	recordKind  = "Server"
	tokenName   = "token"
	serversDir  = "servers"
	probeWait   = 2 * time.Second
	stopWait    = 8 * time.Second
	shutdownFor = 5 * time.Second
)

func recordAPIVersion() string { return home.Org + "/mankit/server/v1" }

type record struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   recordMetadata `json:"metadata"`
	Spec       recordSpec     `json:"spec"`
	file       string
}

type recordMetadata struct {
	App  string `json:"app"`
	Addr string `json:"addr"`
	PID  int    `json:"pid"`
}

type recordSpec struct {
	Name      string    `json:"name,omitempty"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	Log       string    `json:"log,omitempty"`
	Detached  bool      `json:"detached"`
}

// status is a record and what its address said just now; only the second is to be believed.
type status struct {
	Record    record `json:"record"`
	Answering bool   `json:"answering"`
}

// stopped is what stopping one server did: Stale when the server was already gone and only its record went.
type stopped struct {
	Addr    string `json:"addr"`
	PID     int    `json:"pid,omitempty"`
	Stale   bool   `json:"stale,omitempty"`
	Stopped bool   `json:"stopped,omitempty"`
}

// serversDir is where the app's records, logs and token live: its data folder (G-STACK-8), then servers.
func (a App) serversDir() (string, error) {
	dir, err := home.ProductData(a.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, serversDir), nil
}

// fileName is the record file for an address: everything outside a small safe set becomes a dash.
func fileName(addr string) string {
	var b strings.Builder
	for _, r := range addr {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String() + ".json"
}

func logName(addr string) string { return strings.TrimSuffix(fileName(addr), ".json") + ".log" }

// writeWhole writes data to path through a temporary file beside it (G-CODE-5).
func writeWhole(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (r record) save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeWhole(dir, filepath.Join(dir, fileName(r.Metadata.Addr)), append(data, '\n'))
}

// removeRecord drops the record for addr; one already gone is not an error, as two commands tidying one dead server are both right.
func removeRecord(dir, addr string) error {
	err := os.Remove(filepath.Join(dir, fileName(addr)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// readRecords is every record in dir, unprobed; a file it cannot read or understand is an error naming it (G-CODE-6).
func readRecords(dir string) ([]record, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read server record %s: %w", path, err)
		}
		var r record
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("server record %s is not JSON: %w; remove it if no server is on its address", path, err)
		}
		if r.APIVersion != recordAPIVersion() || r.Kind != recordKind || r.Metadata.Addr == "" || r.Metadata.PID < 1 {
			return nil, fmt.Errorf("server record %s is not a %s %s with an address and a pid; remove it if no server is on its address", path, recordAPIVersion(), recordKind)
		}
		r.file = path
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metadata.Addr < out[j].Metadata.Addr })
	return out, nil
}

// live is every record whose server is there; a record whose address refuses a connection is removed, a busy one is kept.
func (a App) live(dir string) ([]status, error) {
	all, err := readRecords(dir)
	if err != nil {
		return nil, err
	}
	out := []status{}
	for _, r := range all {
		p := a.probe(r.Metadata.Addr, probeWait)
		if p.refused {
			_ = os.Remove(r.file)
			continue
		}
		if p.version != "" {
			r.Spec.Version = p.version
		}
		out = append(out, status{Record: r, Answering: p.answering})
	}
	return out, nil
}

// find is the record a target names: its address or the name of what it serves; more than one is refused with the list.
func (a App) find(recs []record, target string) (record, error) {
	var hits []record
	for _, r := range recs {
		if r.Metadata.Addr == target || r.Spec.Name != "" && r.Spec.Name == target {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return record{}, fmt.Errorf("no server here is on %q; `%s servers` lists them", target, a.command())
	case 1:
		return hits[0], nil
	}
	var addrs []string
	for _, h := range hits {
		addrs = append(addrs, h.Metadata.Addr)
	}
	return record{}, fmt.Errorf("%q names %d servers (%s); give the address", target, len(hits), strings.Join(addrs, ", "))
}

var errNoToken = errors.New("this machine has no server token yet: serve makes one")

// readToken is the token every server of the app on this machine asks for; one another user could read or have written is refused.
func readToken(dir string) (string, error) {
	path := filepath.Join(dir, tokenName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoToken
	}
	if err != nil {
		return "", err
	}
	if err := private(info); err != nil {
		return "", fmt.Errorf("%s %w: remove it and serve makes a new one", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// ensureToken reads the token, making it on first use; it stays across restarts.
func ensureToken(dir string) (string, error) {
	if t, err := readToken(dir); !errors.Is(err, errNoToken) {
		return t, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(hex.EncodeToString(raw) + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// A link never replaces: two servers starting at once both keep the one that landed first.
	if err := os.Link(tmp.Name(), filepath.Join(dir, tokenName)); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return readToken(dir)
}

// probed is what one look at an address found: refused (nothing listens, the only verdict that removes a record) or answering as this app.
type probed struct {
	refused   bool
	answering bool
	version   string
}

// probe asks addr's GET /api/version without the token, which never goes to a listener not yet known to be this app.
// This app answers it with token_refused from its guard, and says in ran which app and version it is.
func (a App) probe(addr string, timeout time.Duration) probed {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return probed{refused: refused(err)}
	}
	conn.Close()
	resp, err := (&http.Client{Timeout: timeout}).Get("http://" + addr + "/api/version")
	if err != nil {
		return probed{}
	}
	defer resp.Body.Close()
	var e Envelope
	if resp.StatusCode != http.StatusUnauthorized || json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil ||
		len(e.Errors) != 1 || e.Errors[0].Code != CodeToken || e.Ran.App != a.ID || e.Ran.Version == "" {
		return probed{}
	}
	return probed{answering: true, version: e.Ran.Version}
}

// refused reports whether a dial failed because nothing listens, by its error number (G-STACK-15).
func refused(err error) bool {
	return slices.ContainsFunc(connRefused, func(n error) bool { return errors.Is(err, n) })
}
