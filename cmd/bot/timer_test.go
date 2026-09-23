package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testSnapshot(used, weekly int, reset int64) usageSnapshot {
	return usageSnapshot{FiveHour: usageWindow{used, reset}, Weekly: usageWindow{weekly, reset + 7*24*3600}}
}

func TestClassifyTimer(t *testing.T) {
	base := time.Unix(1900000000, 0)
	oldRolling := testSnapshot(0, 10, base.Add(timerWindow).Unix())
	oldActive := testSnapshot(5, 10, base.Add(timerWindow).Unix())
	cases := []struct {
		name string
		old  *usageSnapshot
		at   time.Time
		new  usageSnapshot
		want timerStatus
	}{
		{"first zero", nil, base.Add(5 * time.Minute), testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), timerUnknown},
		{"first used", nil, base.Add(5 * time.Minute), testSnapshot(1, 10, base.Add(timerWindow).Unix()), timerActive},
		{"fixed zero", &oldRolling, base.Add(5 * time.Minute), testSnapshot(0, 10, oldRolling.FiveHour.ResetsAt), timerActive},
		{"rolling zero", &oldRolling, base.Add(5 * time.Minute), testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), timerInactive},
		{"rolling tolerance", &oldRolling, base.Add(5 * time.Minute), testSnapshot(0, 10, base.Add(timerWindow+6*time.Minute).Unix()), timerInactive},
		{"middle", &oldRolling, base.Add(5 * time.Minute), testSnapshot(0, 10, base.Add(timerWindow+2*time.Minute).Unix()), timerUnknown},
		{"rolling used contradiction", &oldRolling, base.Add(5 * time.Minute), testSnapshot(1, 10, base.Add(timerWindow+5*time.Minute).Unix()), timerUnknown},
		{"used fixed", &oldActive, base.Add(5 * time.Minute), testSnapshot(6, 10, oldActive.FiveHour.ResetsAt), timerActive},
		{"too soon zero", &oldRolling, base.Add(2 * time.Minute), testSnapshot(0, 10, base.Add(timerWindow+2*time.Minute).Unix()), timerUnknown},
		{"cycle crossed", &oldRolling, base.Add(timerWindow), testSnapshot(0, 10, base.Add(2*timerWindow).Unix()), timerUnknown},
		{"cycle crossed used", &oldRolling, base.Add(timerWindow), testSnapshot(1, 10, base.Add(2*timerWindow).Unix()), timerUnknown},
		{"five hours apart", &oldRolling, base.Add(timerWindow), testSnapshot(0, 10, base.Add(2*timerWindow).Unix()), timerUnknown},
		{"clock reversed", &oldRolling, base.Add(-time.Minute), testSnapshot(0, 10, base.Add(timerWindow-time.Minute).Unix()), timerUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTimer(tc.old, base, tc.new, tc.at); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProbeAccountsFailureRetainsEvidenceAndHomesAreIsolated(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	accounts := []accountConfig{{ID: "one", Home: "one"}, {ID: "two", Home: "two"}}
	old := testSnapshot(0, 100, base.Add(timerWindow).Unix())
	states := map[string]accountState{
		"one": {Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base},
		"two": {Home: filepath.Join(root, "two"), LastUsage: &old, LastSuccess: base},
	}
	var homes []string
	probe := func(_ context.Context, _, home string) (usageSnapshot, error) {
		homes = append(homes, home)
		if home == filepath.Join(root, "two") {
			return usageSnapshot{}, errors.New("unavailable")
		}
		return testSnapshot(0, 100, base.Add(timerWindow+5*time.Minute).Unix()), nil
	}
	probeAccounts(context.Background(), accounts, root, "codex", states, probe, nil,
		func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if len(homes) != 2 || homes[0] != filepath.Join(root, "one") || homes[1] != filepath.Join(root, "two") {
		t.Fatalf("wrong homes: %v", homes)
	}
	if states["one"].Timer != timerInactive || states["two"].Timer != timerUnknown || !states["two"].Failed ||
		states["two"].LastUsage != &old {
		t.Fatalf("wrong account states: %+v", states)
	}
	// A later successful poll compares against the preserved read.
	accounts = accounts[1:]
	probe = func(context.Context, string, string) (usageSnapshot, error) {
		return testSnapshot(0, 100, base.Add(timerWindow+10*time.Minute).Unix()), nil
	}
	probeAccounts(context.Background(), accounts, root, "codex", states, probe, nil,
		func() time.Time { return base.Add(10 * time.Minute) }, nil)
	if states["two"].Timer != timerInactive || states["two"].Failed {
		t.Fatalf("preserved evidence was lost: %+v", states["two"])
	}
}

func TestHelloGateRecheckAndCooldown(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	account := []accountConfig{{ID: "one", Home: "one"}}
	old := testSnapshot(0, 99, base.Add(timerWindow).Unix())
	states := map[string]accountState{"one": {Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
	reads, sent, published := 0, 0, 0
	probe := func(ctx context.Context, _, _ string) (usageSnapshot, error) {
		reads++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > probeTimeout {
			t.Fatal("usage read did not get its own 30-second deadline")
		}
		if reads == 2 && published != 0 {
			t.Fatal("Discord publication delayed the immediate recheck")
		}
		if reads == 3 {
			return testSnapshot(0, 99, base.Add(timerWindow+10*time.Minute).Unix()), nil
		}
		return testSnapshot(0, 99, base.Add(timerWindow+5*time.Minute).Unix()), nil
	}
	hello := func(ctx context.Context, _, home string) error {
		sent++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > helloTimeout || home != filepath.Join(root, "one") {
			t.Fatalf("wrong hello context or home: %v", home)
		}
		return context.DeadlineExceeded
	}
	now := func() time.Time { return base.Add(5 * time.Minute) }
	probeAccounts(context.Background(), account, root, "codex", states, probe, hello, now, func() { published++ })
	state := states["one"]
	if reads != 2 || sent != 1 || published != 1 || state.Timer != timerUnknown ||
		state.HelloAt.IsZero() || state.LastUsage.FiveHour.UsedPercent != 0 {
		t.Fatalf("attempt did not recheck and mark unknown: reads=%d sent=%d published=%d state=%+v", reads, sent, published, state)
	}
	probeAccounts(context.Background(), account, root, "codex", states, probe, hello,
		func() time.Time { return base.Add(10 * time.Minute) }, nil)
	if sent != 1 || states["one"].Timer != timerInactive {
		t.Fatalf("cooldown failed: sent=%d state=%+v", sent, states["one"])
	}
}

func TestHelloGateWeeklyAndOtherPlatform(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	accounts := []accountConfig{{ID: "one", Home: "one"}}
	old := testSnapshot(0, 100, base.Add(timerWindow).Unix())
	states := map[string]accountState{"one": {Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
	sent := 0
	hello := func(context.Context, string, string) error { sent++; return nil }
	probeAccounts(context.Background(), accounts, root, "codex", states,
		func(context.Context, string, string) (usageSnapshot, error) {
			return testSnapshot(0, 100, base.Add(timerWindow+5*time.Minute).Unix()), nil
		}, hello, func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if states["one"].Timer != timerInactive || sent != 0 {
		t.Fatalf("weekly cap changed classification or sent hello: %+v", states["one"])
	}
	// Another platform starts the timer before this bot has sent anything.
	states["one"] = accountState{Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}
	probeAccounts(context.Background(), accounts, root, "codex", states,
		func(context.Context, string, string) (usageSnapshot, error) {
			return testSnapshot(0, 10, old.FiveHour.ResetsAt), nil
		}, hello, func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if states["one"].Timer != timerActive || sent != 0 || !states["one"].HelloAt.IsZero() {
		t.Fatalf("external activation sent hello: %+v", states["one"])
	}
}

func TestHomeChangeAndRestartLoseEvidence(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
	states := map[string]accountState{"one": {Home: filepath.Join(root, "old"), LastUsage: &old, LastSuccess: base,
		LastAttempt: base, HelloAt: base}}
	probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: "new"}}, root, "codex", states,
		func(context.Context, string, string) (usageSnapshot, error) {
			return testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), nil
		}, nil, func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if states["one"].Timer != timerUnknown || !states["one"].LastAttempt.IsZero() || !states["one"].HelloAt.IsZero() {
		t.Fatalf("home change kept evidence: %+v", states["one"])
	}
	// A fresh map after restart also requires a second zero-percent read.
	states = make(map[string]accountState)
	probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: "new"}}, root, "codex", states,
		func(context.Context, string, string) (usageSnapshot, error) {
			return testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), nil
		}, nil, func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if states["one"].Timer != timerUnknown {
		t.Fatal(states["one"])
	}
}

func TestRecheckFailureKeepsPreSendUsage(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
	states := map[string]accountState{"one": {Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
	reads := 0
	probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: "one"}}, root, "codex", states,
		func(context.Context, string, string) (usageSnapshot, error) {
			reads++
			if reads == 2 {
				return usageSnapshot{}, errors.New("recheck down")
			}
			return testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), nil
		}, func(context.Context, string, string) error { return nil },
		func() time.Time { return base.Add(5 * time.Minute) }, nil)
	state := states["one"]
	if !state.Failed || state.Timer != timerUnknown || !state.LastSuccess.Equal(base.Add(5*time.Minute)) ||
		state.LastUsage.FiveHour.ResetsAt != base.Add(timerWindow+5*time.Minute).Unix() {
		t.Fatalf("failed recheck lost pre-send usage: %+v", state)
	}
}

func TestRestartCanSendAgainAfterTwoReads(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	accounts := []accountConfig{{ID: "one", Home: "one"}}
	states := make(map[string]accountState) // restored disk state contains no account data
	sent := 0
	hello := func(context.Context, string, string) error { sent++; return nil }
	probe := func(reset int64) usageProbe {
		return func(context.Context, string, string) (usageSnapshot, error) {
			return testSnapshot(0, 10, reset), nil
		}
	}
	probeAccounts(context.Background(), accounts, root, "codex", states,
		probe(base.Add(timerWindow).Unix()), hello, func() time.Time { return base }, nil)
	if sent != 0 || states["one"].Timer != timerUnknown {
		t.Fatalf("first read triggered hello: %+v", states["one"])
	}
	probeAccounts(context.Background(), accounts, root, "codex", states,
		probe(base.Add(timerWindow+5*time.Minute).Unix()), hello,
		func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if sent != 1 {
		t.Fatalf("second read did not trigger hello: %+v", states["one"])
	}
}

func TestRecheckUsedConfirmsActiveAndHelloExpires(t *testing.T) {
	base := time.Unix(1900000000, 0)
	root := t.TempDir()
	old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
	states := map[string]accountState{"one": {Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
	reads := 0
	probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: "one"}}, root, "codex", states,
		func(context.Context, string, string) (usageSnapshot, error) {
			reads++
			if reads == 2 {
				return testSnapshot(1, 10, base.Add(timerWindow+5*time.Minute).Unix()), nil
			}
			return testSnapshot(0, 10, base.Add(timerWindow+5*time.Minute).Unix()), nil
		}, func(context.Context, string, string) error { return nil },
		func() time.Time { return base.Add(5 * time.Minute) }, nil)
	state := states["one"]
	if state.Timer != timerActive || state.HelloAt.IsZero() {
		t.Fatalf("positive recheck did not confirm active: %+v", state)
	}
	state.expireHello(base.Add(timerWindow + 5*time.Minute))
	if !state.HelloAt.IsZero() {
		t.Fatalf("old hello timestamp remains: %+v", state)
	}
}
