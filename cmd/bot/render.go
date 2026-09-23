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

func renderAccount(name string, snapshot *usageSnapshot, lastSuccess time.Time, failed bool) string {
	if snapshot == nil {
		if failed {
			return "🔴 " + name + "\n5-Hour   —\nWeekly   —\nUpdate failed"
		}
		return "⚪ " + name + "\n5-Hour   —\nWeekly   —\nInitializing..."
	} else {
		icon, update := "🟢", "Updated"
		if failed {
			icon, update = "🔴", "Update failed · Last updated"
		}
		fiveHourRemaining := remainingPercent(snapshot.FiveHour.UsedPercent)
		weeklyRemaining := remainingPercent(snapshot.Weekly.UsedPercent)
		return fmt.Sprintf("%s %s\n5-Hour   %s  %d%%   Reset <t:%d:t> (<t:%d:R>)\nWeekly   %s  %d%%   Reset <t:%d:d> (<t:%d:R>)\n%s <t:%d:R>",
			icon, name,
			usageBar(fiveHourRemaining), fiveHourRemaining, snapshot.FiveHour.ResetsAt, snapshot.FiveHour.ResetsAt,
			usageBar(weeklyRemaining), weeklyRemaining, snapshot.Weekly.ResetsAt, snapshot.Weekly.ResetsAt,
			update, lastSuccess.Unix())
	}
}

func renderDashboard(accounts []accountConfig, states map[string]accountState, failed map[string]bool) string {
	sections := make([]string, 0, len(accounts)+1)
	sections = append(sections, dashboardTitle)
	for _, account := range accounts {
		state := states[account.ID]
		sections = append(sections, renderAccount(account.Name, state.LastUsage, state.LastSuccess, failed[account.ID]))
	}
	return strings.Join(sections, "\n\n")
}
