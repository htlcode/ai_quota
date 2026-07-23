package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

type CodexStats struct {
	Available    bool
	Session      QuotaInfo
	Weekly       QuotaInfo
	PlanType     string
	ResetCredits int // -1 = unknown
	Error        string
}

type rpcMsg struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type rateLimitsResult struct {
	RateLimits               codexRateLimits                 `json:"rateLimits"`
	RateLimitsByLimitID      map[string]codexRateLimits      `json:"rateLimitsByLimitId"`
	RateLimitsSnake          codexRateLimitsSnake            `json:"rate_limits"`
	RateLimitsByLimitIDSnake map[string]codexRateLimitsSnake `json:"rate_limits_by_limit_id"`
	ResetCredits             *resetCreditsSummary            `json:"rateLimitResetCredits"`
	ResetCreditsSnake        *resetCreditsSummary            `json:"rate_limit_reset_credits"`
}

type codexRateLimits struct {
	Primary   *codexRateLimitWindow `json:"primary"`
	Secondary *codexRateLimitWindow `json:"secondary"`
	PlanType  string                `json:"planType"`
}

type codexRateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	ResetsAt           int64   `json:"resetsAt"`
	WindowDurationMins int64   `json:"windowDurationMins"`
}

type codexRateLimitsSnake struct {
	Primary   *codexRateLimitWindowSnake `json:"primary"`
	Secondary *codexRateLimitWindowSnake `json:"secondary"`
	PlanType  string                     `json:"plan_type"`
}

type codexRateLimitWindowSnake struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetsAt           int64   `json:"resets_at"`
	WindowDurationMins int64   `json:"window_duration_mins"`
}

type resetCreditsSummary struct {
	AvailableCount      int `json:"availableCount"`
	AvailableCountSnake int `json:"available_count"`
}

func fetchCodex() CodexStats {
	cmd := exec.Command("codex", "app-server")
	// Own process group so killing reaches the native app-server child
	// even when `codex` is the npm wrapper.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return CodexStats{Error: "codex pipe error"}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CodexStats{Error: "codex pipe error"}
	}

	if err := cmd.Start(); err != nil {
		return CodexStats{Error: "codex not found"}
	}
	defer func() {
		if cmd.Process != nil {
			if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
		}
		_ = cmd.Wait()
	}()

	send := func(id *int, method string, params string) error {
		msg := map[string]any{"method": method}
		if id != nil {
			msg["id"] = *id
		}
		if params != "" {
			msg["params"] = json.RawMessage(params)
		} else {
			msg["params"] = json.RawMessage("{}")
		}
		b, _ := json.Marshal(msg)
		_, err := fmt.Fprintf(stdin, "%s\n", b)
		return err
	}

	id1, id2 := 1, 2

	send(&id1, "initialize", `{"clientInfo":{"name":"ai_quota","version":"1.0.0"}}`)
	send(nil, "initialized", "")
	send(&id2, "account/rateLimits/read", "")

	result := make(chan rateLimitsResult, 1)
	errCh := make(chan string, 1)

	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					errCh <- "read error"
				}
				return
			}
			var msg rpcMsg
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg.ID == nil || *msg.ID != id2 {
				continue
			}
			if msg.Error != nil {
				errCh <- "RPC error"
				return
			}
			var r rateLimitsResult
			if err := json.Unmarshal(msg.Result, &r); err != nil {
				errCh <- "parse error"
				return
			}
			result <- r
			return
		}
	}()

	select {
	case r := <-result:
		return buildCodexStats(r)
	case e := <-errCh:
		return CodexStats{Error: e}
	case <-time.After(10 * time.Second):
		return CodexStats{Error: "codex timeout"}
	}
}

func buildCodexStats(r rateLimitsResult) CodexStats {
	rl := r.RateLimits
	if codexLimits, ok := r.RateLimitsByLimitID["codex"]; ok {
		rl = codexLimits
	}
	stats := CodexStats{
		Available:    true,
		PlanType:     rl.PlanType,
		ResetCredits: -1,
		Session:      QuotaInfo{Label: "Session", Percent: -1},
		Weekly:       QuotaInfo{Label: "Weekly", Percent: -1},
	}

	if stats.PlanType == "" {
		snakeLimits := r.RateLimitsSnake
		if codexLimits, ok := r.RateLimitsByLimitIDSnake["codex"]; ok {
			snakeLimits = codexLimits
		}
		stats.PlanType = snakeLimits.PlanType
	}

	if rl.Primary != nil || rl.Secondary != nil {
		if rl.Primary != nil {
			if isCodexWeeklyWindow(rl.Primary.WindowDurationMins) {
				stats.Weekly = codexQuota("Weekly", rl.Primary.UsedPercent, rl.Primary.ResetsAt)
			} else {
				stats.Session = codexQuota("Session", rl.Primary.UsedPercent, rl.Primary.ResetsAt)
			}
		}
		if rl.Secondary != nil {
			stats.Weekly = codexQuota("Weekly", rl.Secondary.UsedPercent, rl.Secondary.ResetsAt)
		}
	} else {
		snakeLimits := r.RateLimitsSnake
		if codexLimits, ok := r.RateLimitsByLimitIDSnake["codex"]; ok {
			snakeLimits = codexLimits
		}
		if snakeLimits.Primary != nil {
			if isCodexWeeklyWindow(snakeLimits.Primary.WindowDurationMins) {
				stats.Weekly = codexQuota("Weekly", snakeLimits.Primary.UsedPercent, snakeLimits.Primary.ResetsAt)
			} else {
				stats.Session = codexQuota("Session", snakeLimits.Primary.UsedPercent, snakeLimits.Primary.ResetsAt)
			}
		}
		if snakeLimits.Secondary != nil {
			stats.Weekly = codexQuota("Weekly", snakeLimits.Secondary.UsedPercent, snakeLimits.Secondary.ResetsAt)
		}
	}

	stats.ResetCredits = codexResetCredits(r)

	return stats
}

func isCodexWeeklyWindow(windowDurationMins int64) bool {
	return windowDurationMins == 7*24*60
}

func codexQuota(label string, usedPercent float64, resetsAt int64) QuotaInfo {
	q := QuotaInfo{Label: label, Percent: int(100 - usedPercent)}
	if q.Percent < 0 {
		q.Percent = 0
	}
	if q.Percent > 100 {
		q.Percent = 100
	}
	if resetsAt > 0 {
		q.ResetsAt = time.Unix(resetsAt, 0)
	}
	return q
}

func codexResetCredits(r rateLimitsResult) int {
	if r.ResetCredits != nil {
		return r.ResetCredits.availableCount()
	}
	if r.ResetCreditsSnake != nil {
		return r.ResetCreditsSnake.availableCount()
	}
	return -1
}

func (r resetCreditsSummary) availableCount() int {
	if r.AvailableCountSnake > 0 {
		return r.AvailableCountSnake
	}
	return r.AvailableCount
}
