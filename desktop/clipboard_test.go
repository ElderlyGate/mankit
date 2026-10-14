package desktop

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestWriteOSC52EncodesText(t *testing.T) {
	var b bytes.Buffer
	if err := writeOSC52(&b, "hello", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), base64.StdEncoding.EncodeToString([]byte("hello"))) {
		t.Fatalf("got %q", b.String())
	}
}

func TestWriteOSC52WrapsForTmux(t *testing.T) {
	var plain, wrapped bytes.Buffer
	_ = writeOSC52(&plain, "x", false)
	_ = writeOSC52(&wrapped, "x", true)
	if wrapped.String() == plain.String() {
		t.Fatal("tmux sequence should be wrapped for passthrough")
	}
}

func captureOSC52(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	saved := osc52Out
	t.Cleanup(func() { osc52Out = saved })
	osc52Out = &b
	return &b
}

func TestCopyStopsAtFirstWorkingBackend(t *testing.T) {
	captureOSC52(t)
	var tried []string
	saved := clipboardBackends
	t.Cleanup(func() { clipboardBackends = saved })
	clipboardBackends = []func(string) error{
		func(string) error { tried = append(tried, "a"); return errors.New("no") },
		func(string) error { tried = append(tried, "b"); return nil },
		func(string) error { tried = append(tried, "c"); return nil },
	}
	if err := Copy("x"); err != nil || strings.Join(tried, ",") != "a,b" {
		t.Fatalf("err %v tried %v", err, tried)
	}
}

func TestCopyFallsBackToOSC52Result(t *testing.T) {
	out := captureOSC52(t)
	defer func() {
		if out.Len() == 0 {
			t.Error("OSC 52 was not written")
		}
	}()
	saved := clipboardBackends
	t.Cleanup(func() { clipboardBackends = saved })
	clipboardBackends = []func(string) error{func(string) error { return errors.New("no") }}
	if err := Copy("x"); err != nil {
		t.Fatalf("OSC 52 to stderr should succeed: %v", err)
	}
}
