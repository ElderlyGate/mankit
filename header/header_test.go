package header

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLocationShortensFromTheLeft(t *testing.T) {
	plain := lipgloss.NewStyle()
	if got := Location("/home/ada/src/repo", "/home/ada", 40, plain, plain); got != "~/src/repo" {
		t.Fatalf("home as ~: %q", got)
	}
	if got := Location("/very/long/parent/repo", "", 10, plain, plain); got != "…rent/repo" {
		t.Fatalf("the last element always shows: %q", got)
	}
}
