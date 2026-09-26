package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCodexCancellationStopsDescendants(t *testing.T) {
	for _, mode := range []string{"probe", "hello"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "codex")
			pidFile := filepath.Join(dir, "child.pid")
			script := "#!/bin/sh\nsleep 30 &\nchild=$!\nprintf '%s' \"$child\" > \"$CHILD_PID_FILE\"\nwait \"$child\"\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CHILD_PID_FILE", pidFile)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if mode == "hello" {
					done <- sendHello(ctx, bin, dir)
				} else {
					_, err := probeUsage(ctx, bin, dir)
					done <- err
				}
			}()
			var pid int
			for pid == 0 {
				b, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(string(b))
				if ctx.Err() != nil {
					t.Fatal("child did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			defer syscall.Kill(pid, syscall.SIGKILL)
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected cancellation failure")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not return promptly")
			}
			// An exited orphan may briefly be a zombie until the host init reaps
			// it. It must not still be running or holding the parent's pipes.
			b, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
			if err == nil && !strings.HasPrefix(strings.TrimSpace(string(b)), "Z") {
				t.Fatalf("descendant survived cancellation: pid=%d stat=%s", pid, b)
			}
		})
	}
}

func TestCodexStderrIsBoundedAndDoesNotExposeRawText(t *testing.T) {
	s := &codexStderr{}
	for _, chunk := range []string{strings.Repeat("secret", 2000), "Resource temporarily ", "unavailable (os error 11) token=private-value"} {
		if n, err := s.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("write: n=%d err=%v", n, err)
		}
		if len(s.tail) > stderrLimit {
			t.Fatalf("unbounded stderr: %d bytes", len(s.tail))
		}
	}
	if got := s.summary(); got != "resource temporarily unavailable" {
		t.Fatalf("unexpected summary: %q", got)
	}
	s = &codexStderr{}
	_, _ = s.Write([]byte("unknown error with private-value"))
	if strings.Contains(s.summary(), "private-value") {
		t.Fatal("unknown stderr leaked")
	}
}
