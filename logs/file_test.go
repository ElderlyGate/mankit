package logs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v4/logs"
)

// A rotation that cannot open the new file leaves no closed file behind: the next write opens it, and nothing is written to a closed file.
func TestALogFileOpensAgainWhenARotationCouldNot(t *testing.T) {
	defer logs.Rotation(50, 5)()
	path := filepath.Join(t.TempDir(), "x.log")
	f, err := logs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	one, two, three := strings.Repeat("1", 40)+"\n", strings.Repeat("2", 40)+"\n", strings.Repeat("3", 40)+"\n"
	if _, err := f.Write([]byte(one)); err != nil {
		t.Fatal(err)
	}
	// The rotation moves the file along, then cannot open the new one, nor can the write after it.
	logs.FailOpen(t, 2)
	if n, err := f.Write([]byte(two)); err == nil || n != 0 {
		t.Fatalf("a write with no file to go to = %d, %v; want an error and nothing written", n, err)
	}
	if _, err := f.Write([]byte(three)); err != nil {
		t.Fatalf("the next write did not open the file again: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != three {
		t.Errorf("the file holds %q, want the write after the failed rotation", b)
	}
	if b, _ := os.ReadFile(path + ".1"); string(b) != one {
		t.Errorf("%s.1 holds %q, want the file before the rotation", path, b)
	}
}

// With TakeStd, the process's stdout follows the log file across a rotation, so what a detached server prints lands in the current file. It runs in a child process, so the test's own stdout is left alone.
func TestTakeStdFollowsTheFileAcrossARotation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdout cannot be pointed at a file here")
	}
	path := os.Getenv("LOGS_TAKESTD_CHILD")
	if path != "" {
		defer logs.Rotation(50, 5)()
		f, err := logs.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.TakeStd(); err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(strings.Repeat("a", 30) + "\n"))
		os.Stdout.WriteString("before\n")
		// Past the size, so this record rotates the file first.
		f.Write([]byte(strings.Repeat("b", 30) + "\n"))
		os.Stdout.WriteString("after\n")
		f.Close()
		return
	}
	path = filepath.Join(t.TempDir(), "std.log")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTakeStdFollowsTheFileAcrossARotation$")
	cmd.Env = append(os.Environ(), "LOGS_TAKESTD_CHILD="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child failed: %v\n%s", err, out)
	}
	now, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	if !strings.Contains(string(now), "after\n") || strings.Contains(string(now), "before\n") {
		t.Errorf("the file now holds %q, want what stdout said after the rotation", now)
	}
	if !strings.Contains(string(old), "before\n") || strings.Contains(string(old), "after\n") {
		t.Errorf("%s.1 holds %q, want what stdout said before it", path, old)
	}
}
