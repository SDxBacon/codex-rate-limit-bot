package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const dashboardTitle = "Codex Usage Monitor" // Legacy heading, kept for message recovery.
const legacyDashboardHeading = "**" + dashboardTitle + "**"
const previousDashboardHeading = "## Codex 額度"
const dashboardHeading = "## AI 帳號額度"

var discordMarkdownEscaper = strings.NewReplacer(
	"\\", "\\\\", "*", "\\*", "_", "\\_", "~", "\\~", "`", "\\`", "|", "\\|",
)

func usageBar(percent float64) string {
	filled := int(math.Floor(percent / 10))
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

func remainingPercent(usedPercent float64) float64 {
	return 100 - usedPercent
}

func percentLabel(percent float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(percent, 'f', 1, 64), ".0")
}

func timerLabel(status timerStatus) string {
	switch status {
	case timerActive:
		return "5小時計時器運作中"
	case timerInactive:
		return "5小時計時器未啟動（重設時間滾動）"
	default:
		return "5小時計時器狀態未知"
	}
}

func resetAtLabel(resetsAt int64, absoluteStyle string) string {
	return fmt.Sprintf("將於 <t:%d:%s>（<t:%d:R>）重設", resetsAt, absoluteStyle, resetsAt)
}

func weeklyResetStyle(resetsAt int64, now time.Time) string {
	remaining := time.Unix(resetsAt, 0).Sub(now)
	if remaining > 0 && remaining <= 24*time.Hour {
		return "t"
	}
	return "d"
}

func helloLine(state accountState) string {
	if state.HelloAt.IsZero() {
		return ""
	}
	return fmt.Sprintf("\n-# Bot 嘗試 hello：<t:%d:f>", state.HelloAt.Unix())
}

func renderAccount(name string, state accountState, now time.Time) string {
	snapshot := state.LastUsage
	provider := "Codex"
	if state.Type == accountClaude {
		provider = "Claude"
	}
	heading := "### " + discordMarkdownEscaper.Replace(name) + " · " + provider
	if snapshot == nil {
		if state.Failed {
			return heading + "\n> 🔴 **讀取失敗** · 5小時計時器狀態未知\n> **5 小時**　—\n> **每週**　　—\n-# 尚無成功讀取資料"
		}
		return heading + "\n> ⚪ **等待首次讀取** · 5小時計時器狀態未知\n> **5 小時**　—\n> **每週**　　—\n-# 尚無用量資料"
	}
	status := "🟢 **讀取正常** · " + timerLabel(state.Timer)
	footer := fmt.Sprintf("<t:%d:R> 更新", state.LastSuccess.Unix())
	if state.Type == accountClaude {
		status = "🟡 **CLI 回報** · " + timerLabel(state.Timer)
		if state.Timer == timerInactive {
			status = "🟡 **CLI 回報** · 5小時計時器未啟動"
		}
		footer = fmt.Sprintf("<t:%d:R> 查詢 · 額度可能為快取", state.LastSuccess.Unix())
	}
	if state.Failed {
		status = "🔴 **讀取失敗** · " + timerLabel(timerUnknown)
		footer = "＊上次成功讀取的資料 · " + footer
		if state.Type == accountClaude {
			footer = fmt.Sprintf("＊上次取得的 CLI 資料 · <t:%d:R> 查詢 · 額度可能為快取", state.LastSuccess.Unix())
		}
	}
	fiveStyle := "t"
	if state.Failed {
		fiveStyle = "s"
	}
	weekStyle := "d"
	if snapshot.Weekly.ResetsAt != nil {
		weekStyle = weeklyResetStyle(*snapshot.Weekly.ResetsAt, now)
	}
	hello := helloLine(state)
	return fmt.Sprintf("%s\n> %s\n> **5 小時**　%s\n> **每週**　　%s\n-# %s%s",
		heading, status, renderWindow(snapshot.FiveHour, fiveStyle, state.Failed),
		renderWindow(snapshot.Weekly, weekStyle, state.Failed), footer, hello)
}

func renderWindow(window usageWindow, resetStyle string, stale bool) string {
	percent := "—"
	if window.UsedPercent != nil {
		remaining := remainingPercent(*window.UsedPercent)
		mark := ""
		if stale {
			mark = "＊"
		}
		percent = fmt.Sprintf("`%s`　**%s%% left%s**", usageBar(remaining), percentLabel(remaining), mark)
	}
	reset := "重設時間未知"
	if window.ResetsAt != nil {
		reset = resetAtLabel(*window.ResetsAt, resetStyle)
	} else if window.ResetNotStarted && window.UsedPercent != nil && *window.UsedPercent == 0 {
		reset = "計時器未啟動"
	}
	return percent + " · " + reset
}

func renderDashboard(accounts []accountConfig, states map[string]accountState, now time.Time) string {
	sections := make([]string, 0, len(accounts)+1)
	sections = append(sections, dashboardHeading)
	for _, account := range accounts {
		state := states[account.ID]
		state.Type = account.providerType()
		sections = append(sections, renderAccount(account.Name, state, now))
	}
	return strings.Join(sections, "\n\n")
}
