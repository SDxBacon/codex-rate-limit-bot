package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

type usageWindow struct {
	UsedPercent int   `json:"used_percent"`
	ResetsAt    int64 `json:"resets_at"`
}

type usageSnapshot struct {
	FiveHour usageWindow `json:"five_hour"`
	Weekly   usageWindow `json:"weekly"`
}

type rateWindow struct {
	UsedPercent        *int   `json:"usedPercent"`
	WindowDurationMins *int   `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

type rateBucket struct {
	Primary   *rateWindow `json:"primary"`
	Secondary *rateWindow `json:"secondary"`
}

type rateResult struct {
	RateLimits          *rateBucket            `json:"rateLimits"`
	RateLimitsByLimitID map[string]*rateBucket `json:"rateLimitsByLimitId"`
}

type rpcResponse struct {
	ID     *json.RawMessage `json:"id"`
	Result json.RawMessage  `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseUsage(raw json.RawMessage) (usageSnapshot, error) {
	var result rateResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return usageSnapshot{}, fmt.Errorf("decode rate limits: %w", err)
	}
	bucket := result.RateLimitsByLimitID["codex"]
	if bucket == nil && len(result.RateLimitsByLimitID) == 0 {
		bucket = result.RateLimits
	}
	if bucket == nil {
		return usageSnapshot{}, errors.New("codex rate-limit bucket is missing")
	}
	var snapshot usageSnapshot
	gotFive, gotWeek := false, false
	for _, window := range []*rateWindow{bucket.Primary, bucket.Secondary} {
		if window == nil || window.WindowDurationMins == nil {
			continue
		}
		if *window.WindowDurationMins != 300 && *window.WindowDurationMins != 10080 {
			continue
		}
		if window.UsedPercent == nil || window.ResetsAt == nil ||
			*window.UsedPercent < 0 || *window.UsedPercent > 100 || *window.ResetsAt <= 0 {
			return usageSnapshot{}, errors.New("codex rate-limit window has invalid data")
		}
		value := usageWindow{UsedPercent: *window.UsedPercent, ResetsAt: *window.ResetsAt}
		if *window.WindowDurationMins == 300 {
			snapshot.FiveHour, gotFive = value, true
		} else {
			snapshot.Weekly, gotWeek = value, true
		}
	}
	if !gotFive || !gotWeek {
		return usageSnapshot{}, errors.New("codex 5-hour or weekly rate-limit window is missing")
	}
	return snapshot, nil
}

// probeUsage starts the Codex CLI for one bounded read. A fresh process on each
// poll also recovers automatically if a previous app-server exited or hung.
func probeUsage(ctx context.Context, binary, codexHome string) (snapshot usageSnapshot, probeErr error) {
	cmd := codexCommand(ctx, binary, codexHome, "app-server", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return usageSnapshot{}, err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return usageSnapshot{}, err
	}
	defer stdout.Close()
	stderr := &codexStderr{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return usageSnapshot{}, fmt.Errorf("start codex CLI: %w", err)
	}
	readCtx, stopRead := context.WithCancel(ctx)
	readerDone := make(chan struct{})
	stage := "initialize"
	defer func() {
		// EOF lets the native server shut down and its npm parent reap it.
		_ = stdin.Close()
		stopRead()
		waited := make(chan error, 1)
		go func() {
			<-readerDone
			waited <- cmd.Wait()
		}()
		timer := time.NewTimer(processGrace)
		defer timer.Stop()
		var waitErr error
		select {
		case waitErr = <-waited:
		case <-timer.C:
			_ = killCodexGroup(cmd)
			_ = stdout.Close()
			waitErr = <-waited
		}
		if probeErr != nil {
			probeErr = codexFailure("codex "+stage, probeErr, waitErr, stderr)
		}
	}()
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-readCtx.Done():
				// Drain remaining output while the server shuts down.
			}
		}
		if err := scanner.Err(); err != nil {
			readErr <- err
		} else {
			readErr <- io.EOF
		}
	}()

	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = stdin.Write(append(b, '\n'))
		return err
	}
	read := func(expected int) (json.RawMessage, error) {
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case err := <-readErr:
				return nil, fmt.Errorf("codex app-server closed: %w", err)
			case line := <-lines:
				var response rpcResponse
				if err := json.Unmarshal(line, &response); err != nil {
					return nil, fmt.Errorf("invalid codex app-server JSON: %w", err)
				}
				if response.ID == nil {
					continue // notification
				}
				var id int
				if err := json.Unmarshal(*response.ID, &id); err != nil || id != expected {
					return nil, errors.New("unexpected codex app-server response ID")
				}
				if response.Error != nil {
					return nil, fmt.Errorf("codex app-server error %d: %s", response.Error.Code, response.Error.Message)
				}
				if len(response.Result) == 0 {
					return nil, errors.New("codex app-server returned no result")
				}
				return response.Result, nil
			}
		}
	}

	init := map[string]any{"method": "initialize", "id": 1, "params": map[string]any{
		"clientInfo": map[string]string{"name": "codex_ratelimite_bot", "title": "Codex Ratelimite Bot", "version": "1.0.0"},
	}}
	if err := write(init); err != nil {
		return usageSnapshot{}, err
	}
	if _, err := read(1); err != nil {
		return usageSnapshot{}, err
	}
	stage = "initialized"
	if err := write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return usageSnapshot{}, err
	}
	stage = "account/rateLimits/read"
	if err := write(map[string]any{"method": "account/rateLimits/read", "id": 2,
		"params": map[string]bool{"excludeResetCreditDetails": true}}); err != nil {
		return usageSnapshot{}, err
	}
	result, err := read(2)
	if err != nil {
		return usageSnapshot{}, err
	}
	return parseUsage(result)
}

const probeTimeout = 30 * time.Second
