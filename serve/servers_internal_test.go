package serve

import (
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"
)

// A server on every address is reached at loopback, IPv4 first; any other address as it is written.
func TestAtLoopback(t *testing.T) {
	for addr, want := range map[string][]string{
		"0.0.0.0:7799":   {"127.0.0.1:7799", "[::1]:7799"},
		"[::]:7799":      {"127.0.0.1:7799", "[::1]:7799"},
		":7799":          {"127.0.0.1:7799", "[::1]:7799"},
		"127.0.0.1:7799": {"127.0.0.1:7799"},
		"[::1]:7799":     {"[::1]:7799"},
		"board:7799":     {"board:7799"},
		"not an address": {"not an address"},
	} {
		if got := atLoopback(addr); !slices.Equal(got, want) {
			t.Errorf("atLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

// dialing answers each address with its error, as the machine would.
func dialing(t *testing.T, answers map[string]error) {
	t.Helper()
	was := dial
	dial = func(_, addr string, _ time.Duration) (net.Conn, error) {
		err, ok := answers[addr]
		if !ok {
			return nil, fmt.Errorf("asked %s, which the test did not expect", addr)
		}
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", err)}
	}
	t.Cleanup(func() { dial = was })
}

// With IPv6 off, [::1] cannot be reached at all; with IPv4 refusing, nothing listens, so the record is gone, and stop and serve --detach see it so.
func TestAnEveryAddressServerIsGoneWhenIPv6IsOffAndIPv4Refuses(t *testing.T) {
	dialing(t, map[string]error{"127.0.0.1:7799": syscall.ECONNREFUSED, "[::1]:7799": familyUnreachable[0]})
	if p := (App{ID: "designer"}).probe("[::]:7799", 10*time.Millisecond); !p.refused {
		t.Fatalf("got %+v", p)
	}
}

// A place that neither refuses nor is out of reach, such as one that times out, may hold a server, so the record stays.
func TestAnEveryAddressServerIsKeptWhileOnePlaceMayHoldIt(t *testing.T) {
	timedOut := errors.New("i/o timeout")
	dialing(t, map[string]error{"127.0.0.1:7799": syscall.ECONNREFUSED, "[::1]:7799": timedOut})
	if p := (App{ID: "designer"}).probe("0.0.0.0:7799", 10*time.Millisecond); p.refused {
		t.Fatalf("got %+v", p)
	}
}

// A server on one address that dropped off the network, Wi-Fi gone or DHCP took it back, is not proven gone: its record stays, so stop still finds its pid.
func TestAServerOnAnAddressOffTheNetworkIsKept(t *testing.T) {
	for _, errno := range familyUnreachable {
		dialing(t, map[string]error{"192.168.1.5:7799": errno})
		if p := (App{ID: "designer"}).probe("192.168.1.5:7799", 10*time.Millisecond); p.refused {
			t.Fatalf("%v: got %+v", errno, p)
		}
	}
}

// Every loopback out of reach, with no refusal, proves nothing either.
func TestAnEveryAddressServerIsKeptWhenNoLoopbackRefuses(t *testing.T) {
	dialing(t, map[string]error{"127.0.0.1:7799": syscall.ENETUNREACH, "[::1]:7799": familyUnreachable[0]})
	if p := (App{ID: "designer"}).probe("[::]:7799", 10*time.Millisecond); p.refused {
		t.Fatalf("got %+v", p)
	}
}
