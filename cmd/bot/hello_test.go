package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSendHelloCLIOptionsAndAccountIsolation(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	trace := filepath.Join(dir, "trace")
	script := "#!/bin/sh\nprintf '%s\\n' \"$CODEX_HOME\" \"$@\" >> \"$TRACE_FILE\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_FILE", trace)
	for _, home := range []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")} {
		if err := sendHello(context.Background(), bin, home); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	wantArgs := []string{"exec", "--ephemeral", "--skip-git-repo-check", "--model", "gpt-6-luna",
		"-c", `model_reasoning_effort="low"`, "--sandbox", "read-only", "hello"}
	if len(lines) != 2*(len(wantArgs)+1) {
		t.Fatalf("unexpected trace: %q", b)
	}
	for i, home := range []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")} {
		start := i * (len(wantArgs) + 1)
		if lines[start] != home {
			t.Fatalf("wrong CODEX_HOME: %q", lines[start])
		}
		for j, want := range wantArgs {
			if lines[start+j+1] != want {
				t.Fatalf("arg %d = %q, want %q", j, lines[start+j+1], want)
			}
		}
	}
}

func TestSendHelloRespectsDeadline(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := sendHello(ctx, bin, filepath.Join(dir, "one"))
	if err == nil || time.Since(start) > 2*time.Second || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("deadline not respected: err=%v elapsed=%v", err, time.Since(start))
	}
}
