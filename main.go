package main

import (
	"ai_quota/quota"
	"ai_quota/reports"
	"fmt"
	"sync/atomic"

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
	mCodexResets := systray.AddMenuItem("", "")
	mCodexResets.Hide()

	systray.AddSeparator()

	systray.AddMenuItem("DEEPSEEK", "")
	mDeepSeekBalance := systray.AddMenuItem("   Balance  loading...", "")

	systray.AddSeparator()

	mUsageReports := systray.AddMenuItem("Usage Reports…", "Open token and cost usage")
	mRefresh := systray.AddMenuItem("↻  Refresh", "Refresh now")
	mQuit := systray.AddMenuItem("✕  Quit", "Quit")

	var refreshing atomic.Bool
	var usageReportOpening atomic.Bool
	refresh := func() {
		startedRefresh := refreshing.CompareAndSwap(false, true)
		if !startedRefresh {
			return
		}
		defer refreshing.Store(false)

		mRefresh.Disable()
		systray.SetTitle("AI ↻")

		quotaStats := quota.FetchStats()
		claude := quotaStats.Claude
		codex := quotaStats.Codex
		deepSeek := quotaStats.DeepSeek

		if !claude.Available {
			claudeErrorTitle := "   ⚠ " + claude.Error
			mClaudeSession.SetTitle(claudeErrorTitle)
			mClaudeWeekly.SetTitle("")
		} else {
			claudeSessionTitle := quota.Format("Session", claude.Session)
			mClaudeSession.SetTitle(claudeSessionTitle)
			claudeWeeklyTitle := quota.Format("Weekly ", claude.Weekly)
			mClaudeWeekly.SetTitle(claudeWeeklyTitle)
		}

		if !codex.Available {
			codexErrorTitle := "   ⚠ " + codex.Error
			mCodexSession.SetTitle(codexErrorTitle)
			mCodexWeekly.SetTitle("")
			mCodexResets.Hide()
		} else {
			codexSessionTitle := quota.Format("Session", codex.Session)
			mCodexSession.SetTitle(codexSessionTitle)
			codexWeeklyTitle := quota.Format("Weekly ", codex.Weekly)
			mCodexWeekly.SetTitle(codexWeeklyTitle)
			updateCodexResetCredits(mCodexResets, codex.ResetCredits)
		}

		if !deepSeek.Available {
			deepSeekErrorTitle := "   ⚠ " + deepSeek.Error
			mDeepSeekBalance.SetTitle(deepSeekErrorTitle)
		} else {
			deepSeekBalanceTitle := quota.FormatDeepSeekBalances(deepSeek.Balances)
			mDeepSeekBalance.SetTitle(deepSeekBalanceTitle)
		}

		statusTitle := quota.MenuBarTitle(quotaStats)
		systray.SetTitle(statusTitle)
		mRefresh.Enable()
	}

	go refresh()

	for {
		select {
		case <-mUsageReports.ClickedCh:
			startedOpening := usageReportOpening.CompareAndSwap(false, true)
			if !startedOpening {
				continue
			}
			go func() {
				defer usageReportOpening.Store(false)
				_ = reports.Open()
			}()
		case <-mRefresh.ClickedCh:
			go refresh()
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func updateCodexResetCredits(item *systray.MenuItem, count int) {
	if count < 0 {
		item.Hide()
		return
	}
	title := fmt.Sprintf("   Usage resets available : %d", count)
	item.SetTitle(title)
	item.Show()
}
