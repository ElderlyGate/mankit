package serve_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v2/ask"
	"github.com/tugbayatilla/mankit/v2/home"
	"github.com/tugbayatilla/mankit/v2/serve"
)

// The servers token renew command: a new token, taken by the running servers without a restart.

// Success

func TestServersTokenRenewReachesARunningServer(t *testing.T) {
	machine(t)
	rec := detached(t)
	addr := addrOf(rec)
	old := tokenOf(t, answer(t, linkSchema, "servers", "link", addr))
	if got := apiStatus(t, addr, old); got != http.StatusOK {
		t.Fatalf("before the renew: status %d", got)
	}
	renewed := tokenOf(t, answer(t, linkSchema, "servers", "token", "renew", "--yes"))
	if renewed == old {
		t.Fatal("the token did not change")
	}
	if got := apiStatus(t, addr, old); got != http.StatusUnauthorized {
		t.Fatalf("the old token: status %d, want %d", got, http.StatusUnauthorized)
	}
	if got := apiStatus(t, addr, renewed); got != http.StatusOK {
		t.Fatalf("the new token: status %d", got)
	}
	if got := tokenOf(t, answer(t, linkSchema, "servers", "link", addr)); got != renewed {
		t.Fatal("servers link prints another token than the renewed one")
	}
	list := answer(t, listSchema, "servers").Data.([]any)
	if len(list) != 1 || list[0].(map[string]any)["record"].(map[string]any)["metadata"].(map[string]any)["pid"] != rec["metadata"].(map[string]any)["pid"] {
		t.Fatalf("the server was restarted: %v", list)
	}
}

func TestServersTokenRenewKeepsTheTokenToTheUser(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	if _, err := run(t, "servers", "token", "renew", "--yes"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %04o, want 0600", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".tmp-*")); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func TestServersTokenRenewIsSafeAtOnce(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	h, err := app.Handler(site(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		printed = map[string]bool{}
	)
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			out, err := run(t, "servers", "token", "renew", "--yes")
			if err != nil {
				t.Error(err)
				return
			}
			u, _ := url.Parse(strings.TrimSpace(out))
			mu.Lock()
			printed[u.Query().Get("token")] = true
			mu.Unlock()
		}()
		go func() {
			defer wg.Done()
			call(t, h, "GET", "/api/greeting", "", nil)
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	last := strings.TrimSpace(string(data))
	if len(last) != 64 || !printed[last] || len(printed) != 8 {
		t.Fatalf("the token file holds %q, not one whole token of the %d printed", data, len(printed))
	}
	if w := call(t, h, "GET", "/api/greeting", "", map[string]string{"Authorization": "Bearer " + last}); w.Code != http.StatusOK {
		t.Fatalf("the last token: status %d", w.Code)
	}
}

func TestServersTokenLivesInTheAppsFolder(t *testing.T) {
	for name, c := range map[string]struct{ folder, want string }{
		"its own folder": {"e-designer", "e-designer"},
		"none":           {"", "designer"},
	} {
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			t.Setenv("XDG_DATA_HOME", data)
			a := app
			a.Folder = c.folder
			if _, err := a.Handler(site(nil), nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(data, home.Org, c.want, "servers", "token")); err != nil {
				t.Fatalf("no token under %s: %v", c.want, err)
			}
		})
	}
}

// Contract

func TestServersTokenRenewRefusesAContractItDoesNotAnswerBeforeItRenews(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	_, err := run(t, "servers", "token", "renew", "--yes", "-o", "json", "--contract", "v9")
	var e *serve.Error
	if !errors.As(err, &e) || e.Code != serve.CodeUnsupportedContract {
		t.Fatalf("got %v", err)
	}
	unchanged(t, dir)
}

// Errors and exit codes

func TestServersTokenRenewIsRefusedWithoutATerminalOrYes(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	if _, err := run(t, "servers", "token", "renew"); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("got %v, want a refusal", err)
	}
	unchanged(t, dir)
}

// Asking

func TestServersTokenRenewAsksForTheName(t *testing.T) {
	dir := machine(t)
	writeToken(t, dir, token)
	var asked []ask.Action
	r := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	cmds, err := app.Commands(func(*cobra.Command) (serve.Site, error) { return site(nil), nil }, func(_ *cobra.Command, a ask.Action) error {
		asked = append(asked, a)
		return ask.ErrRefused
	})
	if err != nil {
		t.Fatal(err)
	}
	r.AddCommand(cmds...)
	r.SetOut(&bytes.Buffer{})
	r.SetArgs([]string{"servers", "token", "renew"})
	if err := r.Execute(); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("got %v", err)
	}
	if len(asked) != 1 || !asked[0].System || asked[0].Name != "e-designer" || asked[0].Type != ask.Connect {
		t.Fatalf("asked %+v, want the name e-designer for a change to the system itself", asked)
	}
	unchanged(t, dir)
}

// CLI and HTTP parity

func TestServersTokenRenewAnswersLikeHTTP(t *testing.T) {
	machine(t)
	sameAsHTTP(t, "", "servers", "token", "renew", "--yes")
}

// Help

func TestServersTokenRenewHelpSaysWhatItChanges(t *testing.T) {
	out := help(t, "servers", "token", "renew")
	for _, want := range []string{"next request", "servers link", "asks for the name", "--yes", "--output", "--contract"} {
		if !strings.Contains(out, want) {
			t.Errorf("servers token renew --help has no %s:\n%s", want, out)
		}
	}
}

// tokenOf is the token in a Link answer's url.
func tokenOf(t *testing.T, e serve.Envelope) string {
	t.Helper()
	u, err := url.Parse(e.Data.(map[string]any)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("token")
}

// apiStatus is the status of one API request to the server at addr with bearer.
func apiStatus(t *testing.T, addr, bearer string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/version", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// unchanged fails the test unless the machine's token is still token.
func unchanged(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil || strings.TrimSpace(string(data)) != token {
		t.Fatalf("the token changed: %q, %v", data, err)
	}
}
