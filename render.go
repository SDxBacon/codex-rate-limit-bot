package main

import (
	"fmt"
	"strings"
	"time"
)

const dashboardTitle = "Codex Usage Monitor"

func usageBar(percent int) string {
	filled := percent / 10
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

func remainingPercent(usedPercent int) int {
	return 100 - usedPercent
}

func renderDashboard(snapshot *usageSnapshot, lastSuccess time.Time, failed bool) string {
	var account1 string
	if snapshot == nil {
		account1 = "⚪ Account 1\n5-Hour   —\nWeekly   —\nInitializing..."
	} else {
		icon, update := "🟢", "Updated"
		if failed {
			icon, update = "🔴", "Update failed · Last updated"
		}
		fiveHourRemaining := remainingPercent(snapshot.FiveHour.UsedPercent)
		weeklyRemaining := remainingPercent(snapshot.Weekly.UsedPercent)
		account1 = fmt.Sprintf("%s Account 1\n5-Hour   %s  %d%%   Reset <t:%d:t> (<t:%d:R>)\nWeekly   %s  %d%%   Reset <t:%d:d> (<t:%d:R>)\n%s <t:%d:R>",
			icon,
			usageBar(fiveHourRemaining), fiveHourRemaining, snapshot.FiveHour.ResetsAt, snapshot.FiveHour.ResetsAt,
			usageBar(weeklyRemaining), weeklyRemaining, snapshot.Weekly.ResetsAt, snapshot.Weekly.ResetsAt,
			update, lastSuccess.Unix())
	}
	return dashboardTitle + "\n\n" + account1 +
		"\n\n⚪ Account 2\n5-Hour   —\nWeekly   —\nInitializing..." +
		"\n\n🔴 Account 3\n5-Hour   ██░░░░░░░░  28%   Reset 00:03 (in 22m)\nWeekly   ░░░░░░░░░░  7%   Reset Sep 24 (in 1d)\nUpdate failed · Last updated 29m ago"
}
