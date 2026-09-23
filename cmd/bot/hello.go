package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const helloTimeout = 90 * time.Second

type helloRunner func(context.Context, string, string) error

func sendHello(ctx context.Context, binary, codexHome string) error {
	cmd := exec.CommandContext(ctx, binary, "exec", "--ephemeral", "--skip-git-repo-check",
		"--model", "gpt-6-luna", "-c", "model_reasoning_effort=\"low\"",
		"--sandbox", "read-only", "hello")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codex hello: %w", err)
	}
	return nil
}
