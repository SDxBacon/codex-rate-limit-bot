package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestProvidersShareHelloLifecycleAndCooldown(t *testing.T) {
	for _, kind := range []string{accountCodex, accountClaude} {
		for _, outcome := range []string{"active", "zero", "missing percent", "missing reset", "recheck failure", "hello failure"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				root := t.TempDir()
				base := time.Unix(1900000000, 0)
				old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
				state := accountState{Type: kind, Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}
				states := map[string]accountState{"one": state}
				account := accountConfig{ID: "one", Home: "one", Type: accountType(kind)}
				now := base.Add(5 * time.Minute)
				reads, hellos, publishes := 0, 0, 0
				probe := func(ctx context.Context, _, _ string) (usageSnapshot, error) {
					reads++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > probeTimeout {
						t.Fatal("probe deadline")
					}
					s := testSnapshot(0, 10, now.Add(timerWindow).Unix())
					if kind == accountClaude && reads != 2 {
						s = testClaudeIdleSnapshot(10)
					}
					if reads == 2 {
						if publishes != 0 {
							t.Fatal("publication delayed recheck")
						}
						switch outcome {
						case "active":
							s.FiveHour.UsedPercent = newUsageWindow(1, 1).UsedPercent
						case "missing percent":
							s.FiveHour.UsedPercent = nil
						case "missing reset":
							s.FiveHour.ResetsAt = nil
						case "recheck failure":
							return usageSnapshot{}, errors.New("read_failed")
						}
					}
					return s, nil
				}
				hello := func(ctx context.Context, _, home string) error {
					hellos++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > helloTimeout || home != state.Home {
						t.Fatal("hello context")
					}
					if outcome == "hello failure" {
						return context.DeadlineExceeded
					}
					return nil
				}
				providers := map[string]accountProvider{kind: {Probe: probe, Hello: hello}}
				probeAccounts(context.Background(), []accountConfig{account}, root, states, providers, func() time.Time { return now }, func() { publishes++ })
				got := states["one"]
				if reads != 2 || hellos != 1 || publishes != 1 || got.LastAttempt.IsZero() || got.HelloAt.IsZero() {
					t.Fatalf("lifecycle failed: %+v", got)
				}
				wantTimer := timerUnknown
				if outcome == "active" || (kind == accountClaude && (outcome == "zero" || outcome == "hello failure")) {
					wantTimer = timerActive
				}
				if got.Timer != wantTimer || got.Failed != (outcome == "recheck failure") {
					t.Fatalf("wrong recheck: %+v", got)
				}
				// A settings-only reload must preserve the attempt and cooldown.
				account.HelloModel = "opus"
				now = now.Add(5 * time.Minute)
				probeAccounts(context.Background(), []accountConfig{account}, root, states, providers, func() time.Time { return now }, nil)
				if hellos != 1 || states["one"].LastAttempt != got.LastAttempt {
					t.Fatal("cooldown lost")
				}
			})
		}
	}
}

func TestCodexRejectsIncompleteAndNonIdleHelloEvidence(t *testing.T) {
	for _, kind := range []string{accountCodex} {
		for _, name := range []string{"weekly missing", "weekly full", "reset missing", "reset expired", "percent missing", "fixed zero", "used", "cross reset", "short interval"} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				base := time.Unix(1900000000, 0)
				old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
				now := base.Add(5 * time.Minute)
				current := testSnapshot(0, 10, now.Add(timerWindow).Unix())
				switch name {
				case "weekly missing":
					current.Weekly.UsedPercent = nil
				case "weekly full":
					current.Weekly.UsedPercent = newUsageWindow(100, 1).UsedPercent
				case "reset missing":
					current.FiveHour.ResetsAt = nil
				case "reset expired":
					current.FiveHour.ResetsAt = newUsageWindow(0, now.Add(-time.Minute).Unix()).ResetsAt
				case "percent missing":
					current.FiveHour.UsedPercent = nil
				case "fixed zero":
					current.FiveHour.ResetsAt = old.FiveHour.ResetsAt
				case "used":
					current.FiveHour.UsedPercent = newUsageWindow(1, 1).UsedPercent
				case "cross reset":
					old.FiveHour.ResetsAt = newUsageWindow(0, now.Add(-time.Minute).Unix()).ResetsAt
				case "short interval":
					now = base.Add(time.Minute)
				}
				states := map[string]accountState{"one": {Type: kind, Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
				sent := 0
				providers := map[string]accountProvider{kind: {Probe: func(context.Context, string, string) (usageSnapshot, error) { return current, nil }, Hello: func(context.Context, string, string) error { sent++; return nil }}}
				probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: "one", Type: accountType(kind)}}, root, states, providers, func() time.Time { return now }, nil)
				if sent != 0 || !states["one"].HelloAt.IsZero() {
					t.Fatal("unexpected greeting")
				}
			})
		}
	}
}

func TestProductionProvidersDefaultHelloLifecycle(t *testing.T) {
	for _, kind := range []string{accountCodex, accountClaude} {
		t.Run(kind, func(t *testing.T) {
			providers := makeProviders("codex", "claude")
			provider := providers[kind]
			if provider.Probe == nil || provider.Hello == nil {
				t.Fatal("provider must support both usage and hello by default")
			}
			base := time.Unix(1900000000, 0)
			root := t.TempDir()
			old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
			states := map[string]accountState{"one": {Type: kind, Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
			reads, sent := 0, 0
			provider.Probe = func(context.Context, string, string) (usageSnapshot, error) {
				reads++
				if kind == accountClaude {
					return testClaudeIdleSnapshot(10), nil
				}
				return testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), nil
			}
			hello := func(context.Context, string, string) error { sent++; return nil }
			provider.Hello = hello
			if provider.ConfigureHello != nil {
				provider.ConfigureHello = func(accountConfig) helloRunner { return hello }
			}
			providers[kind] = provider
			probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: "one", Type: accountType(kind)}}, root, states, providers, func() time.Time { return base.Add(5 * time.Minute) }, nil)
			if sent != 1 || reads != 2 || states["one"].LastAttempt.IsZero() {
				t.Fatal("default provider did not send hello and recheck")
			}
		})
	}
}
