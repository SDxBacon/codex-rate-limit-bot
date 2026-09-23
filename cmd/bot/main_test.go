package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeAccountsIndependentFailure(t *testing.T) {
	accounts := []accountConfig{{ID: "one", Home: "one"}, {ID: "two", Home: "two"}}
	old := &usageSnapshot{FiveHour: usageWindow{1, 100}, Weekly: usageWindow{2, 200}}
	state := savedState{Accounts: map[string]accountState{"two": {LastUsage: old, LastSuccess: time.Unix(100, 0)}}}
	root := t.TempDir()
	var homes []string
	probe := func(_ context.Context, _, home string) (usageSnapshot, error) {
		homes = append(homes, home)
		if home == filepath.Join(root, "two") {
			return usageSnapshot{}, errors.New("unavailable")
		}
		return usageSnapshot{FiveHour: usageWindow{82, 300}, Weekly: usageWindow{61, 400}}, nil
	}
	failed, success := probeAccounts(context.Background(), accounts, root, "codex", &state, probe, func() time.Time { return time.Unix(500, 0) })
	if !success || failed["one"] || !failed["two"] || len(homes) != 2 ||
		homes[0] != filepath.Join(root, "one") || homes[1] != filepath.Join(root, "two") {
		t.Fatalf("unexpected probe result: failed=%v homes=%v", failed, homes)
	}
	if state.Accounts["one"].LastUsage.FiveHour.UsedPercent != 82 || state.Accounts["two"].LastUsage != old {
		t.Fatalf("account data not isolated: %+v", state.Accounts)
	}
}

func TestStateSaveInterval(t *testing.T) {
	for raw, want := range map[string]time.Duration{"": 30 * time.Minute, "1h": time.Hour, "5m": 5 * time.Minute} {
		got, err := parseStateSaveInterval(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"0", "-1m", "later"} {
		if _, err := parseStateSaveInterval(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	path := filepath.Join(t.TempDir(), "state.json")
	state := savedState{MessageID: "123"}
	var last time.Time
	start := time.Unix(1000, 0)
	persistUsageIfDue(path, state, start, &last, 30*time.Minute, true)
	if !last.Equal(start) {
		t.Fatal("first success was not saved")
	}
	state.MessageID = "456"
	persistUsageIfDue(path, state, start.Add(25*time.Minute), &last, 30*time.Minute, true)
	disk, err := loadState(path)
	if err != nil || disk.MessageID != "123" {
		t.Fatalf("saved before interval: %+v, %v", disk, err)
	}
	persistUsageIfDue(path, state, start.Add(30*time.Minute), &last, 30*time.Minute, true)
	disk, err = loadState(path)
	if err != nil || disk.MessageID != "456" {
		t.Fatalf("not saved at interval: %+v, %v", disk, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	persistUsageIfDue(path, state, start.Add(time.Hour), &last, 30*time.Minute, false)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failure should not save: %v", err)
	}
}
