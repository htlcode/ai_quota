package quota

import (
	"fmt"
	"time"
)

// Format renders one quota window for the tray menu.
func Format(label string, quota QuotaInfo) string {
	if quota.Percent < 0 {
		unknownTitle := fmt.Sprintf("   %s   ▱▱▱▱▱▱▱▱▱▱  n/a", label)
		return unknownTitle
	}
	bar := progressBar(quota.Percent, 10)
	reset := ""
	resetIsUnknown := quota.ResetsAt.IsZero()
	if !resetIsUnknown {
		resetText := formatReset(quota.ResetsAt)
		reset = "  ·  " + resetText
	}
	title := fmt.Sprintf("   %s   %s  %3d%%%s", label, bar, quota.Percent, reset)
	return title
}

// MenuBarTitle reports whether every tray provider returned usable data.
func MenuBarTitle(stats Stats) string {
	claude := stats.Claude
	if !claude.Available {
		return "🔴 AI"
	}
	if claude.Session.Percent < 0 {
		if claude.Weekly.Percent < 0 {
			return "🔴 AI"
		}
	}
	codex := stats.Codex
	if !codex.Available {
		return "🔴 AI"
	}
	if codex.Session.Percent < 0 {
		if codex.Weekly.Percent < 0 {
			return "🔴 AI"
		}
	}
	deepSeek := stats.DeepSeek
	if !deepSeek.Available {
		return "🔴 AI"
	}
	deepSeekBalanceCount := len(deepSeek.Balances)
	if deepSeekBalanceCount == 0 {
		return "🔴 AI"
	}
	return "🟢 AI"
}

func formatReset(resetAt time.Time) string {
	now := time.Now()
	resetLayout := "2006/01/02 at 15:04"
	if resetAt.Year() == now.Year() {
		if resetAt.YearDay() == now.YearDay() {
			resetLayout = "15:04"
		}
	}
	resetTime := resetAt.Format(resetLayout)
	message := fmt.Sprintf("resets %s", resetTime)
	return message
}

func progressBar(percent int, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := percent * width / 100
	bar := ""
	for index := 0; index < width; index++ {
		if index < filled {
			bar += "▰"
		} else {
			bar += "▱"
		}
	}
	return bar
}
