package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"
)

func main() {
	systray.Run(onReady, nil)
}

func onReady() {
	systray.SetTitle("AI")
	systray.SetTooltip("AI Quota Monitor")

	systray.AddMenuItem("CLAUDE", "")
	mClaudeSession := systray.AddMenuItem("   Session  loading...", "")
	mClaudeWeekly := systray.AddMenuItem("   Weekly   loading...", "")

	systray.AddSeparator()

	systray.AddMenuItem("CODEX", "")
	mCodexSession := systray.AddMenuItem("   Session  loading...", "")
	mCodexWeekly := systray.AddMenuItem("   Weekly   loading...", "")

	systray.AddSeparator()

	mRefresh := systray.AddMenuItem("↻  Refresh", "Refresh now")
	mQuit := systray.AddMenuItem("✕  Quit", "Quit")

	var refreshing atomic.Bool

	refresh := func() {
		if !refreshing.CompareAndSwap(false, true) {
			return
		}
		defer refreshing.Store(false)

		mRefresh.Disable()
		systray.SetTitle("AI ↻")

		claudeCh := make(chan ClaudeStats, 1)
		codexCh := make(chan CodexStats, 1)

		go func() { claudeCh <- fetchClaude() }()
		go func() { codexCh <- fetchCodex() }()

		claude := <-claudeCh
		codex := <-codexCh

		// Claude
		if !claude.Available {
			mClaudeSession.SetTitle("   ⚠ " + claude.Error)
			mClaudeWeekly.SetTitle("")
		} else {
			mClaudeSession.SetTitle(formatQuota("Session", claude.Session))
			mClaudeWeekly.SetTitle(formatQuota("Weekly ", claude.Weekly))
		}

		// Codex
		if !codex.Available {
			mCodexSession.SetTitle("   ⚠ " + codex.Error)
			mCodexWeekly.SetTitle("")
		} else {
			mCodexSession.SetTitle(formatQuota("Session", codex.Session))
			mCodexWeekly.SetTitle(formatQuota("Weekly ", codex.Weekly))
		}

		systray.SetTitle(menuBarTitle(claude, codex))
		mRefresh.Enable()
	}

	go refresh()

	for {
		select {
		case <-mRefresh.ClickedCh:
			go refresh()
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func formatQuota(label string, q QuotaInfo) string {
	if q.Percent < 0 {
		return fmt.Sprintf("   %s   ▱▱▱▱▱▱▱▱▱▱  n/a", label)
	}
	bar := progressBar(q.Percent, 10)
	reset := ""
	if !q.ResetsAt.IsZero() {
		reset = "  ·  " + formatReset(q.ResetsAt)
	}
	return fmt.Sprintf("   %s   %s  %3d%%%s", label, bar, q.Percent, reset)
}

// formatReset returns "resets HH:MM" if same day, else "resets YYYY/MM/DD at HH:MM".
func formatReset(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return fmt.Sprintf("resets %s", t.Format("15:04"))
	}
	return fmt.Sprintf("resets %s", t.Format("2006/01/02 at 15:04"))
}

// progressBar uses ▰/▱ (Black/White Sesame Dot) — both Geometric Shapes,
// guaranteed same width in proportional fonts so bars align perfectly.
func progressBar(pct, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	filled := pct * width / 100

	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "▰"
		} else {
			bar += "▱"
		}
	}
	return bar
}

// menuBarTitle: green if all probes returned data, red if any failed.
// Never shows percentage.
func menuBarTitle(c ClaudeStats, cx CodexStats) string {
	claudeOK := c.Available && (c.Session.Percent >= 0 || c.Weekly.Percent >= 0)
	codexOK := cx.Available && (cx.Session.Percent >= 0 || cx.Weekly.Percent >= 0)

	if claudeOK && codexOK {
		return "🟢 AI"
	}
	return "🔴 AI"
}
