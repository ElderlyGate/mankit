package serve_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v3/serve"
)

// The servers link command: the link that opens a server's page.

const linkSchema = "mankit/v1/link.schema.json"

// Success

func TestServersLinkOpensThePage(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	e := answer(t, linkSchema, "servers", "link", addr)
	link := e.Data.(map[string]any)["url"].(string)
	if e.Kind != serve.LinkKind || !strings.HasPrefix(link, "http://"+addr+"/?token=") {
		t.Fatalf("link %q", link)
	}
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(resp.Cookies()) != 1 {
		t.Fatalf("status %d, cookies %v", resp.StatusCode, resp.Cookies())
	}
	out, err := run(t, "servers", "link", addr)
	if err != nil || strings.TrimSpace(out) != link {
		t.Fatalf("table %q, %v", out, err)
	}
}

func TestServersLinkStartsAtTheBaseGiven(t *testing.T) {
	machine(t)
	addr := addrOf(detached(t))
	own := answer(t, linkSchema, "servers", "link", addr).Data.(map[string]any)["url"].(string)
	_, query, _ := strings.Cut(own, "/?")
	for _, base := range []string{"https://board.example.com", "https://board.example.com/", "http://board.example.com:8080"} {
		e := answer(t, linkSchema, "servers", "link", addr, "--base", base)
		want := strings.TrimSuffix(base, "/") + "/?" + query
		if link := e.Data.(map[string]any)["url"].(string); link != want {
			t.Errorf("--base %s: link %q, want %q", base, link, want)
		}
	}
}

// Contract

func TestServersLinkRefusesAContractItDoesNotAnswer(t *testing.T) {
	machine(t)
	detached(t)
	_, err := run(t, "servers", "link", "-o", "json", "--contract", "v9")
	var e *serve.Problem
	if !errors.As(err, &e) || e.Type != serve.TypeUnsupportedContract {
		t.Fatalf("got %v", err)
	}
}

// Errors and exit codes

func TestServersLinkNeedsAToken(t *testing.T) {
	machine(t)
	if _, err := run(t, "servers", "link"); err == nil || !strings.Contains(err.Error(), "no server token") {
		t.Fatalf("got %v", err)
	}
}

func TestServersLinkRefusesABaseThatIsNotASchemeAndHost(t *testing.T) {
	machine(t)
	detached(t)
	for _, base := range []string{"board.example.com", "ftp://board.example.com", "https://", "https://board.example.com/board", "https://board.example.com/?a=b", "https://user@board.example.com"} {
		if out, err := run(t, "servers", "link", "--base", base); err == nil || !strings.Contains(err.Error(), "--base takes") || strings.Contains(out, "token=") {
			t.Errorf("--base %s: %q, %v", base, out, err)
		}
	}
}

// Asking

func TestServersLinkNeverAsks(t *testing.T) {
	machine(t)
	detached(t)
	if _, err := run(t, "servers", "link"); err != nil {
		t.Fatalf("reading asked: %v", err)
	}
}

// CLI and HTTP parity

func TestServersLinkAnswersLikeHTTP(t *testing.T) {
	machine(t)
	detached(t)
	sameAsHTTP(t, "", "servers", "link")
}

// Help

func TestServersLinkHelpNamesItsDefaultAddress(t *testing.T) {
	out := help(t, "servers", "link")
	for _, want := range []string{"127.0.0.1:0", "--output", "--contract", "--base"} {
		if !strings.Contains(out, want) {
			t.Errorf("servers link --help has no %s:\n%s", want, out)
		}
	}
}
