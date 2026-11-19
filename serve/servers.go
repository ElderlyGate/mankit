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

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/internal/disk"
	"github.com/tugbayatilla/mankit/v4/problem"
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
	dir, err := home.ProductData(a.folder())
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

func (r record) save(dir string) error {
	if _, err := disk.Private(dir); err != nil {
		return err
	}
	data, err := disk.JSON(r)
	if err != nil {
		return err
	}
	return disk.WriteFile(filepath.Join(dir, fileName(r.Metadata.Addr)), data, 0o600)
}

// removeRecord drops the record for addr; one already gone is not an error, as two commands tidying one dead server are both right.
func removeRecord(dir, addr string) error {
	return disk.Remove(filepath.Join(dir, fileName(addr)))
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
			_ = disk.Remove(r.file)
			continue
		}
		if p.version != "" {
			r.Spec.Version = p.version
		}
		out = append(out, status{Record: r, Answering: p.answering})
	}
	return out, nil
}

// one is whether the server at addr answers now, asked at addr alone, with the records for that address; no record is removed, as one this machine cannot reach, from inside a container, may be another's.
func (a App) one(dir, addr string) ([]status, bool, error) {
	all, err := readRecords(dir)
	if err != nil {
		return nil, false, err
	}
	p := a.probe(addr, probeWait)
	at := atLoopback(addr)[0]
	out := []status{}
	for _, r := range all {
		if r.Metadata.Addr == addr || atLoopback(r.Metadata.Addr)[0] == at {
			if p.version != "" {
				r.Spec.Version = p.version
			}
			out = append(out, status{Record: r, Answering: p.answering})
		}
	}
	return out, p.answering, nil
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
		return record{}, fmt.Errorf("no server here is on %q; `%s server list` lists them", target, a.command())
	case 1:
		return hits[0], nil
	}
	var addrs []string
	for _, h := range hits {
		addrs = append(addrs, h.Metadata.Addr)
	}
	return record{}, fmt.Errorf("%q names %d servers (%s); give the address", target, len(hits), strings.Join(addrs, ", "))
}

var errNoToken = errors.New("this machine has no server token yet")

// readToken reads the machine's token whole on every call, checking the folder and the open file, so no link or swapped file is read.
func (a App) readToken(dir string) (string, error) {
	path := filepath.Join(dir, tokenName)
	renew := fmt.Sprintf("`%s server token renew` writes a new one", a.command())
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: `%s serve` or %s", errNoToken, a.command(), renew)
	}
	if err != nil {
		return "", err
	}
	if err := privateDir(info); err != nil {
		return "", fmt.Errorf("%s %w: %s", dir, err, renew)
	}
	f, err := openNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: `%s serve` or %s", errNoToken, a.command(), renew)
	}
	if err != nil {
		return "", fmt.Errorf("%s cannot be read: %w; %s", path, err, renew)
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil {
		return "", err
	}
	if err := private(info); err != nil {
		return "", fmt.Errorf("%s %w: %s", path, err, renew)
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<10))
	if err != nil {
		return "", err
	}
	// An empty token would match a request that carries none.
	t := strings.TrimSpace(string(data))
	if t == "" {
		return "", fmt.Errorf("%s holds no token: %s", path, renew)
	}
	return t, nil
}

// ensureToken reads the token, making it on first use; it stays across restarts.
func (a App) ensureToken(dir string) (string, error) {
	if t, err := a.readToken(dir); !errors.Is(err, errNoToken) {
		return t, err
	}
	if _, err := disk.Private(dir); err != nil {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	// Written once, never replaced: two servers starting at once both keep the one that landed first.
	if err := disk.WriteOnce(filepath.Join(dir, tokenName), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return a.readToken(dir)
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// renewToken replaces the token whole with a new one and reads it back; every running server refuses the old one from its next request.
func (a App) renewToken(dir string) (string, error) {
	if _, err := disk.Private(dir); err != nil {
		return "", err
	}
	// One renew at a time, so the token each prints is the one in the file when it prints it.
	lock := disk.Lock(filepath.Join(dir, ".token.lock"))
	if err := lock.Lock(); err != nil {
		return "", err
	}
	defer lock.Unlock()
	token, err := newToken()
	if err != nil {
		return "", err
	}
	// The temporary file is the user's alone (0600), so the rename never leaves a token others could read.
	if err := disk.WriteFile(filepath.Join(dir, tokenName), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	got, err := a.readToken(dir)
	if err != nil {
		return "", err
	}
	if got != token {
		return "", fmt.Errorf("%s holds another token than the one just written; run the renew again", filepath.Join(dir, tokenName))
	}
	return got, nil
}

// probed is what one look at an address found: refused (nothing listens, the only verdict that removes a record) or answering as this app.
type probed struct {
	refused   bool
	answering bool
	version   string
}

// probe asks addr's GET /api/version without the token, which never goes to a listener not yet known to be this app.
// This app answers it with refused for its token from its guard, and says in ran or source which app and version it is; Envelope reads either envelope, so an app on mankit v2 is still found.
// Left open: another user's process could answer so on a reused port; a signal still reaches only this user's processes, and only on a reused pid.
func (a App) probe(addr string, timeout time.Duration) probed {
	var conn net.Conn
	// Gone only when some place refuses and none may still hold it: a family the machine cannot reach, [::1] with IPv6 off, holds nothing, but alone it proves nothing, as an address that dropped off the network (Wi-Fi, DHCP) may still have its server.
	gone, anyRefused := true, false
	for _, at := range atLoopback(addr) {
		c, err := dial("tcp", at, timeout)
		if err == nil {
			conn, addr = c, at
			break
		}
		anyRefused = anyRefused || refused(err)
		gone = gone && (refused(err) || unreachable(err))
	}
	if conn == nil {
		return probed{refused: gone && anyRefused}
	}
	conn.Close()
	resp, err := (&http.Client{Timeout: timeout}).Get("http://" + addr + "/api/version")
	if err != nil {
		return probed{}
	}
	defer resp.Body.Close()
	var e Envelope
	// An answer with ran: null is not envelope/v1, so UpgradeEnvelope refuses it and the decode fails: not this app.
	if resp.StatusCode != http.StatusUnauthorized || json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil ||
		len(e.Errors) != 1 || e.Errors[0].Type != problem.TypeRefused || e.Errors[0].Reason != ReasonToken || e.Source.App != a.ID || e.Source.Version == "" {
		return probed{}
	}
	return probed{answering: true, version: e.Source.Version}
}

// dial is how the probe connects; a test puts its own in place.
var dial = net.DialTimeout

// atLoopback is where a server at addr is reached from this machine: addr itself, or, for every address (0.0.0.0, [::], :port), 127.0.0.1 then [::1], since only a loopback name passes the guard and a [::] listener takes IPv4 too (macOS records a 0.0.0.0 server as [::]).
func atLoopback(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{addr}
	}
	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		return []string{addr}
	}
	return []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)}
}

// refused reports whether a dial failed because nothing listens, by its error number (G-STACK-15).
func refused(err error) bool {
	return slices.ContainsFunc(connRefused, func(n error) bool { return errors.Is(err, n) })
}

// unreachable reports whether a dial failed because the machine cannot reach that address family at all, by its error number (G-STACK-15).
func unreachable(err error) bool {
	return slices.ContainsFunc(familyUnreachable, func(n error) bool { return errors.Is(err, n) })
}
