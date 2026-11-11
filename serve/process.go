package serve

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The child of serve --detach is told what it is by the environment, never by a flag a user could type.
const (
	detachedEnv  = "MANKIT_SERVE_DETACHED"
	logEnv       = "MANKIT_SERVE_LOG"
	startTimeout = 15 * time.Second
)

// spawn starts exe args detached in dir and waits until it serves and wrote its record, so an address in use fails here, with what the child said.
func spawn(exe string, args []string, dir, recordDir, addr string) (record, error) {
	if err := os.MkdirAll(recordDir, 0o700); err != nil {
		return record{}, err
	}
	logPath := filepath.Join(recordDir, logName(addr))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return record{}, fmt.Errorf("cannot open %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr, cmd.Stdin = logFile, logFile, nil
	cmd.Env = append(os.Environ(), detachedEnv+"=1", logEnv+"="+logPath)
	cmd.SysProcAttr = detachAttrs()
	if err := cmd.Start(); err != nil {
		return record{}, fmt.Errorf("cannot start %s: %w", exe, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	pid := cmd.Process.Pid
	deadline := time.Now().Add(startTimeout)
	for {
		recs, err := readRecords(recordDir)
		if err != nil {
			// The child is not killed: it may be serving well, and only another record is in the way of seeing it.
			return record{}, fmt.Errorf("the server (pid %d) started, but its record cannot be seen: %w", pid, err)
		}
		for _, r := range recs {
			if r.Metadata.PID == pid {
				return r, nil
			}
		}
		select {
		case err := <-exited:
			return record{}, startFailed(err, logPath)
		default:
		}
		if time.Now().After(deadline) {
			// Left running it would be a server without a record, which nothing could find or stop.
			_ = cmd.Process.Kill()
			return record{}, fmt.Errorf("the server did not come up within %s and was stopped%s", startTimeout, logTail(logPath))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func startFailed(waitErr error, logPath string) error {
	tail := logTail(logPath)
	if tail == "" && waitErr != nil {
		return fmt.Errorf("the server exited at once: %w", waitErr)
	}
	if tail == "" {
		return errors.New("the server exited at once and said nothing; see " + logPath)
	}
	return fmt.Errorf("the server exited at once%s", tail)
}

// logTail is the last lines the child wrote: the only account of why it did not start.
func logTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return ":\n  " + strings.Join(lines, "\n  ")
}

// stop shuts one server down with SIGTERM and drops its record; it signals a pid only while the address answers as this app, as a pid is reused.
func (a App) stop(dir string, r record) (stopped, error) {
	out := stopped{Addr: r.Metadata.Addr, PID: r.Metadata.PID}
	if r.Metadata.PID < 1 {
		// A pid below 1 signals a whole process group, or every process this user owns.
		return out, fmt.Errorf("the record for %s has no pid (%d), so nothing is signalled", r.Metadata.Addr, r.Metadata.PID)
	}
	p := a.probe(r.Metadata.Addr, probeWait)
	if p.refused {
		out.Stale = true
		return out, removeRecord(dir, r.Metadata.Addr)
	}
	if !p.answering {
		return out, fmt.Errorf("%s is held by something that does not answer as %s; this will not signal a process it cannot name (recorded pid %d)",
			r.Metadata.Addr, a.command(), r.Metadata.PID)
	}
	proc, err := os.FindProcess(r.Metadata.PID)
	if err != nil {
		return out, fmt.Errorf("pid %d: %w", r.Metadata.PID, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Windows has no SIGTERM; elsewhere a failed signal means the process is already gone, which the wait shows.
		if err := proc.Kill(); err != nil {
			return out, fmt.Errorf("signalling pid %d: %w", r.Metadata.PID, err)
		}
	}
	deadline := time.Now().Add(stopWait)
	for !a.probe(r.Metadata.Addr, 500*time.Millisecond).refused {
		if time.Now().After(deadline) {
			return out, fmt.Errorf("%s was signalled but still listens after %s; pid %d is not shutting down", r.Metadata.Addr, stopWait, r.Metadata.PID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	out.Stopped = true
	return out, removeRecord(dir, r.Metadata.Addr)
}
