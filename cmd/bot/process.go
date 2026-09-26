package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const processGrace = time.Second

// The npm CLI launches a native child. Cancel the whole process group so a
// timeout cannot leave that child running. Docker's init reaps orphaned exits.
func codexCommand(ctx context.Context, binary, home string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killCodexGroup(cmd) }
	cmd.WaitDelay = processGrace
	return cmd
}

func killCodexGroup(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// Keep bounded diagnostic input, but never log raw stderr: it may contain
// credentials or account details. Only fixed, recognized categories are emitted.
type codexStderr struct{ tail []byte }

const stderrLimit = 4096

func (s *codexStderr) Write(p []byte) (int, error) {
	n := len(p)
	if n >= stderrLimit {
		s.tail = append(s.tail[:0], p[n-stderrLimit:]...)
	} else {
		if excess := len(s.tail) + n - stderrLimit; excess > 0 {
			copy(s.tail, s.tail[excess:])
			s.tail = s.tail[:len(s.tail)-excess]
		}
		s.tail = append(s.tail, p...)
	}
	return n, nil
}

// Call only after cmd.Wait, which joins the stderr copying goroutine.
func (s *codexStderr) summary() string {
	text := strings.ToLower(string(s.tail))
	var found []string
	for _, phrase := range []string{
		"resource temporarily unavailable", "cannot allocate memory",
		"no space left on device", "too many open files", "permission denied",
		"read-only file system", "panicked at", "refresh_token_reused",
		"refresh_token_expired", "refresh_token_invalid",
	} {
		if strings.Contains(text, phrase) {
			found = append(found, phrase)
		}
	}
	if len(found) == 0 {
		return "no recognized diagnostic (raw stderr withheld)"
	}
	return strings.Join(found, "; ")
}

func codexFailure(stage string, err, waitErr error, stderr *codexStderr) error {
	return fmt.Errorf("%s: %w; process exit: %v; stderr: %s", stage, err, waitErr, stderr.summary())
}
