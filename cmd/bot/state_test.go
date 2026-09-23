package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyStateMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"message_id":"456","last_usage":{"five_hour":{"used_percent":82,"resets_at":1900000000},"weekly":{"used_percent":61,"resets_at":2000000000}},"last_success":"2026-09-23T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := loadState(path)
	if err != nil || state.MessageID != "456" || state.Accounts["account-1"].LastUsage.FiveHour.UsedPercent != 82 {
		t.Fatalf("legacy state not migrated: %+v, %v", state, err)
	}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == old || state.Accounts["account-1"].LastSuccess.IsZero() {
		t.Fatalf("new state not saved: %s", b)
	}
}
