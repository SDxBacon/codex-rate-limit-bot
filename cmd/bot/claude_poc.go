package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type claudePOCDiagnostics struct {
	EndpointAttempts  int  `json:"endpoint_attempts"`
	EndpointSuccesses int  `json:"endpoint_successes"`
	CacheHit          bool `json:"cache_hit"`
	FetchFailed       bool `json:"fetch_failed"`
}

type claudePOCObservation struct {
	At          time.Time            `json:"observed_at"`
	Usage       usageSnapshot        `json:"usage"`
	Diagnostics claudePOCDiagnostics `json:"diagnostics"`
	Failure     string               `json:"failure,omitempty"`
}

func (o claudePOCObservation) endpointConfirmed() bool {
	return o.Failure == "" && o.Diagnostics.EndpointSuccesses > 0 && !o.Diagnostics.CacheHit && !o.Diagnostics.FetchFailed
}

type claudePOCHello struct {
	At          time.Time `json:"attempted_at"`
	CompletedAt time.Time `json:"completed_at"`
	Model       string    `json:"requested_model"`
	Effort      *string   `json:"requested_effort"`
	Failure     string    `json:"failure,omitempty"`
}

type claudePOCVerdict struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type claudePOCReport struct {
	Schema     int                    `json:"schema"`
	CLIVersion string                 `json:"cli_version"`
	OS         string                 `json:"os"`
	Arch       string                 `json:"arch"`
	UID        int                    `json:"uid"`
	Home       string                 `json:"config_dir"`
	Samples    []claudePOCObservation `json:"samples"`
	Hello      *claudePOCHello        `json:"hello,omitempty"`
	Verdict    claudePOCVerdict       `json:"verdict"`
}

// This is an experiment conclusion, not a freshness interface for production.
// Require a naturally expired window, rolling idle samples, a successful manual
// hello, then two fresh fixed-reset observations near a new five-hour window.
func analyzeClaudePOC(report claudePOCReport) claudePOCVerdict {
	unknown := func(reason string) claudePOCVerdict { return claudePOCVerdict{"unknown", reason} }
	if report.OS != "linux" || report.Arch != "arm64" || report.UID == 0 {
		return unknown("linux_arm64_non_root_validation_required")
	}
	var idle *claudePOCObservation
	for i := 1; i < len(report.Samples); i++ {
		a, b := report.Samples[i-1], report.Samples[i]
		if !a.endpointConfirmed() || !b.endpointConfirmed() ||
			(report.Hello != nil && !b.At.Before(report.Hello.At)) {
			continue
		}
		if classifyTimer(&a.Usage, a.At, b.Usage, b.At) != timerInactive {
			continue
		}
		for _, earlier := range report.Samples[:i-1] {
			if earlier.endpointConfirmed() && earlier.Usage.FiveHour.ResetsAt != nil &&
				earlier.Usage.FiveHour.UsedPercent != nil &&
				earlier.At.Before(time.Unix(*earlier.Usage.FiveHour.ResetsAt, 0)) &&
				!a.At.Before(time.Unix(*earlier.Usage.FiveHour.ResetsAt, 0)) {
				wasRunning := *earlier.Usage.FiveHour.UsedPercent > 0
				for _, prior := range report.Samples[:i-1] {
					if prior.endpointConfirmed() && prior.At.Before(earlier.At) &&
						classifyTimer(&prior.Usage, prior.At, earlier.Usage, earlier.At) == timerActive {
						wasRunning = true
					}
				}
				if !wasRunning {
					continue
				}
				copy := b
				idle = &copy
				break
			}
		}
	}
	if idle == nil {
		return unknown("expired_window_and_fresh_rolling_idle_samples_required")
	}
	if report.Hello == nil {
		return unknown("manual_hello_required")
	}
	if report.Hello.Failure != "" || report.Hello.CompletedAt.Before(report.Hello.At) ||
		!report.Hello.At.After(idle.At) {
		return unknown("successful_manual_hello_required")
	}
	if report.Hello.Model != "sonnet" || report.Hello.Effort == nil || *report.Hello.Effort != "low" {
		return unknown("sonnet_low_validation_required")
	}
	for i := 1; i < len(report.Samples); i++ {
		a, b := report.Samples[i-1], report.Samples[i]
		if !a.endpointConfirmed() || !b.endpointConfirmed() || a.At.Before(report.Hello.CompletedAt) ||
			a.Usage.FiveHour.UsedPercent == nil || b.Usage.FiveHour.UsedPercent == nil ||
			a.Usage.FiveHour.ResetsAt == nil || b.Usage.FiveHour.ResetsAt == nil {
			continue
		}
		reset := time.Unix(*a.Usage.FiveHour.ResetsAt, 0)
		delta := time.Duration(*b.Usage.FiveHour.ResetsAt-*a.Usage.FiveHour.ResetsAt) * time.Second
		elapsed := b.At.Sub(a.At)
		if elapsed >= minimumComparison && elapsed < timerWindow && b.At.Before(reset) && near(delta, 0) &&
			!near(delta, elapsed) && near(reset.Sub(report.Hello.At), timerWindow) &&
			classifyTimer(&a.Usage, a.At, b.Usage, b.At) == timerActive {
			return claudePOCVerdict{"validated", "fresh_expired_to_rolling_to_hello_to_fixed_window_observed"}
		}
	}
	return unknown("fresh_fixed_new_window_samples_after_hello_required")
}

func probeClaudePOC(ctx context.Context, binary, home string) claudePOCObservation {
	observation := claudePOCObservation{}
	dir, err := os.MkdirTemp("", "claude-poc-")
	if err != nil {
		observation.Failure = "temporary_directory_unavailable"
		observation.At = time.Now().UTC()
		return observation
	}
	defer os.RemoveAll(dir)
	debug := filepath.Join(dir, "debug.log")
	readCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	observation.Usage, err = probeClaudeUsageWithOptions(readCtx, binary, home, claudeSessionOptions{DebugFile: debug})
	cancel()
	observation.At = time.Now().UTC()
	if err != nil {
		observation.Failure = err.Error()
	}
	f, err := os.Open(debug)
	if err != nil {
		return observation
	}
	defer f.Close()
	// A truncated diagnostic file cannot confirm fresh evidence.
	data, err := io.ReadAll(io.LimitReader(f, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 {
		return observation
	}
	raw := string(data)
	observation.Diagnostics = claudePOCDiagnostics{
		EndpointAttempts:  strings.Count(raw, "fetchUtilization: GET /api/oauth/usage"),
		EndpointSuccesses: strings.Count(raw, "fetchUtilization: 200"),
		CacheHit:          strings.Contains(raw, "Usage read answered from a snapshot"),
		FetchFailed:       strings.Contains(raw, "Failed to load usage data") || strings.Contains(raw, "Usage fetch returned a fieldless"),
	}
	return observation
}

func saveClaudePOC(path string, report claudePOCReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.New("poc_report_encode_failed")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".claude-poc-*")
	if err != nil {
		return errors.New("poc_report_create_failed")
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("poc_report_write_failed")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return errors.New("poc_report_replace_failed")
	}
	return nil
}

func runClaudePOC(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("claude-poc", flag.ContinueOnError)
	home := flags.String("config-dir", "", "Authenticated directory as seen inside this container")
	output := flags.String("output", "", "Report file in a writable mounted directory (0600)")
	previous := flags.String("compare", "", "Continue a prior report for this profile, CLI version and platform")
	binary := flags.String("binary", "claude", "Claude executable")
	hello := flags.Bool("hello", false, "Explicitly send ONE Sonnet/low greeting, consuming subscription quota")
	watch := flags.Bool("watch", false, "Sample repeatedly; only --hello sends a greeting, once")
	interval := flags.Duration("interval", pollInterval, "Sampling interval; minimum 3m")
	duration := flags.Duration("duration", 6*time.Hour, "Bounded watch duration; maximum 12h")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("poc_invalid_arguments")
	}
	if flags.NArg() != 0 || *home == "" || *output == "" || *interval < minimumComparison || *duration <= 0 || *duration > 12*time.Hour {
		return errors.New("poc requires --config-dir and --output; interval >= 3m, duration > 0 and <= 12h")
	}
	profile, err := filepath.Abs(*home)
	if err != nil {
		return errors.New("poc_invalid_home")
	}
	info, err := os.Stat(profile)
	if err != nil || !info.IsDir() {
		return errors.New("poc_profile_directory_unavailable (check symlink target mounts)")
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		return errors.New("poc_invalid_output")
	}
	if *previous == "" {
		if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("poc_output_exists; use --compare to continue it")
		}
	}
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	cmd := cliCommand(versionCtx, *binary, "--version")
	cmd.Env = claudeEnvironment(profile)
	bounded := &cliStderr{}
	cmd.Stdout, cmd.Stderr = bounded, io.Discard
	err = cmd.Run()
	cancel()
	if err != nil {
		return errors.New("poc_cli_version_unavailable")
	}
	version := regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\b`).FindString(string(bounded.tail))
	if version == "" {
		return errors.New("poc_cli_version_unavailable")
	}
	report := claudePOCReport{Schema: 1, CLIVersion: version, OS: runtime.GOOS, Arch: runtime.GOARCH, UID: os.Getuid(), Home: profile}
	if *previous != "" {
		f, err := os.Open(*previous)
		if err != nil {
			return errors.New("poc_previous_report_unavailable")
		}
		data, readErr := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
		_ = f.Close()
		var prior claudePOCReport
		if readErr != nil || len(data) > 4*1024*1024 || json.Unmarshal(data, &prior) != nil ||
			prior.Schema != 1 || prior.CLIVersion != version || prior.OS != report.OS || prior.Arch != report.Arch ||
			prior.UID != report.UID || prior.Home != report.Home || len(prior.Samples) > 1000 {
			return errors.New("poc_previous_report_invalid_or_different_profile_version_platform")
		}
		report = prior
	}
	if *hello && report.Hello != nil {
		return errors.New("poc_hello_already_attempted; do not repeat within this experiment")
	}
	persist := func() error {
		report.Verdict = analyzeClaudePOC(report)
		if err := saveClaudePOC(outputPath, report); err != nil {
			return err
		}
		fmt.Printf("samples=%d status=%s reason=%s\n", len(report.Samples), report.Verdict.Status, report.Verdict.Reason)
		return nil
	}
	// Verify report writability before starting any CLI operation or greeting.
	if err := persist(); err != nil {
		return err
	}
	deadline := time.Now().Add(*duration)
	first := true
	for {
		if ctx.Err() != nil {
			return nil
		}
		if len(report.Samples) >= 1000 {
			return errors.New("poc_sample_limit_reached")
		}
		started := time.Now()
		observation := probeClaudePOC(ctx, *binary, profile)
		report.Samples = append(report.Samples, observation)
		if err := persist(); err != nil {
			return err
		}
		if first && *hello {
			if !observation.endpointConfirmed() || observation.Usage.Weekly.UsedPercent == nil || *observation.Usage.Weekly.UsedPercent >= 100 {
				return errors.New("poc_hello_skipped: fresh_usage_and_available_weekly_quota_required")
			}
			options, _ := (accountConfig{}).claudeHelloOptions()
			report.Hello = &claudePOCHello{At: time.Now().UTC(), Model: options.Model, Effort: options.Effort}
			// Persist the attempt first; interrupted experiments must not resend it.
			report.Hello.Failure = "attempt_in_progress_or_interrupted"
			if err := persist(); err != nil {
				return err
			}
			helloCtx, cancel := context.WithTimeout(ctx, helloTimeout)
			err := sendClaudeHello(helloCtx, *binary, profile, options)
			cancel()
			report.Hello.CompletedAt = time.Now().UTC()
			report.Hello.Failure = ""
			if err != nil {
				report.Hello.Failure = err.Error()
			}
			if err := persist(); err != nil {
				return err
			}
			report.Samples = append(report.Samples, probeClaudePOC(ctx, *binary, profile))
			if err := persist(); err != nil {
				return err
			}
			if err != nil {
				return errors.New("poc_hello_failed (see sanitized report)")
			}
		}
		first = false
		if !*watch || !started.Add(*interval).Before(deadline) {
			return nil
		}
		timer := time.NewTimer(time.Until(started.Add(*interval)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
