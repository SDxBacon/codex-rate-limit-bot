package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type claudeSessionOptions struct {
	Hello     *helloOptions
	DebugFile string // PoC only; never enabled by the monitor.
}

type claudeEvent struct {
	line []byte
	err  error
}

type claudeSession struct {
	ctx       context.Context
	stdin     io.WriteCloser
	events    <-chan claudeEvent
	stage     string
	requestID int
}

// Each operation owns a fresh process. Closing stdin lets CLI wrappers reap
// their native children; cancellation and the grace deadline kill the group.
func withClaudeSession(ctx context.Context, binary, home string, options claudeSessionOptions,
	operation func(*claudeSession) error) (operationErr error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return errors.New("claude setup: invalid_home")
	}
	if strings.ContainsRune(binary, filepath.Separator) {
		binary, err = filepath.Abs(binary)
		if err != nil {
			return errors.New("claude setup: invalid_binary")
		}
	}
	work, err := os.MkdirTemp("", "claude-monitor-")
	if err != nil {
		return errors.New("claude setup: temporary_directory_unavailable")
	}
	defer os.RemoveAll(work)
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--safe-mode", "--tools", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--permission-mode", "dontAsk"}
	if options.Hello != nil {
		args = append(args, "--model", options.Hello.Model, "--system-prompt", "Reply to greetings briefly. Do not use tools.")
		if options.Hello.Effort != nil {
			args = append(args, "--effort", *options.Hello.Effort)
		}
	}
	if options.DebugFile != "" {
		args = append(args, "--debug-file", options.DebugFile)
	}
	cmd := cliCommand(ctx, binary, args...)
	cmd.Dir, cmd.Env = work, claudeEnvironment(home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("claude setup: stdin_unavailable")
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("claude setup: stdout_unavailable")
	}
	defer stdout.Close()
	stderr := &cliStderr{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return claudeFailure("start", err, nil, stderr)
	}
	readCtx, stopRead := context.WithCancel(ctx)
	events := make(chan claudeEvent)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			select {
			case events <- claudeEvent{line: append([]byte(nil), scanner.Bytes()...)}:
			case <-readCtx.Done(): // Drain while the CLI exits.
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case events <- claudeEvent{err: err}:
		case <-readCtx.Done():
		}
	}()
	session := &claudeSession{ctx: ctx, stdin: stdin, events: events, stage: "initialize"}
	defer func() {
		_ = stdin.Close()
		stopRead()
		waited := make(chan error, 1)
		go func() { <-readerDone; waited <- cmd.Wait() }()
		timer := time.NewTimer(processGrace)
		defer timer.Stop()
		var waitErr error
		forced := false
		select {
		case waitErr = <-waited:
		case <-timer.C:
			forced = true
			_ = killProcessGroup(cmd)
			_ = stdout.Close()
			waitErr = <-waited
		}
		if operationErr == nil && options.Hello != nil && waitErr != nil && !forced {
			operationErr = errors.New("process_exit")
		}
		if operationErr != nil {
			operationErr = claudeFailure(session.stage, operationErr, waitErr, stderr)
		}
	}()
	return operation(session)
}

func (s *claudeSession) send(frame any) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := json.NewEncoder(s.stdin).Encode(frame); err != nil {
		return errors.New("stdin_write_failed")
	}
	return nil
}

func (s *claudeSession) next() (map[string]json.RawMessage, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case event := <-s.events:
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if event.err != nil {
			return nil, event.err
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(event.line, &frame); err != nil || frame == nil {
			return nil, errors.New("invalid_control_json")
		}
		var kind string
		if err := json.Unmarshal(frame["type"], &kind); err != nil || kind == "" {
			return nil, errors.New("invalid_control_json")
		}
		return frame, nil
	}
}

func (s *claudeSession) request(subtype string, skipBehaviors bool) (json.RawMessage, error) {
	s.stage = subtype
	s.requestID++
	id := strconv.Itoa(s.requestID)
	fields := map[string]any{"subtype": subtype}
	if skipBehaviors {
		fields["skip_behaviors"] = true
	}
	if err := s.send(map[string]any{"type": "control_request", "request_id": id, "request": fields}); err != nil {
		return nil, err
	}
	for {
		frame, err := s.next()
		if err != nil {
			return nil, err
		}
		if string(frame["type"]) != `"control_response"` {
			continue
		}
		var response struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
			Error     json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(frame["response"], &response); err != nil {
			return nil, errors.New("invalid_control_json")
		}
		if response.RequestID != id {
			continue
		}
		if response.Subtype != "success" {
			return nil, errors.New(claudeControlError(response.Error))
		}
		if len(response.Response) == 0 || string(response.Response) == "null" {
			return nil, errors.New("empty_control_response")
		}
		return response.Response, nil
	}
}

func makeProviders(codexBinary, claudeBinary string) map[string]accountProvider {
	return map[string]accountProvider{
		accountCodex: {Binary: codexBinary, Probe: probeUsage, Hello: sendHello},
		accountClaude: {
			Binary: claudeBinary, Probe: probeClaudeUsage,
			Hello:          claudeHelloForAccount(accountConfig{}),
			ConfigureHello: claudeHelloForAccount,
		},
	}
}

func claudeHelloForAccount(account accountConfig) helloRunner {
	options, err := account.claudeHelloOptions()
	return func(ctx context.Context, binary, home string) error {
		if err != nil {
			return errors.New("claude hello: invalid_settings")
		}
		return sendClaudeHello(ctx, binary, home, options)
	}
}
