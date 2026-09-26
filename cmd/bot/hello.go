package main

import (
	"context"
	"io"
	"time"
)

const helloTimeout = 90 * time.Second

type helloRunner func(context.Context, string, string) error

func sendHello(ctx context.Context, binary, codexHome string) error {
	cmd := codexCommand(ctx, binary, codexHome, "exec", "--ephemeral", "--skip-git-repo-check",
		"--model", "gpt-6-luna", "-c", "model_reasoning_effort=\"low\"",
		"--sandbox", "read-only", "hello")
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	stderr := &codexStderr{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return codexFailure("codex hello", err, err, stderr)
	}
	return nil
}
