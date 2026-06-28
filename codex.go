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
	Available bool
	Session   QuotaInfo
	Weekly    QuotaInfo
	PlanType  string
	Error     string
}

type rpcMsg struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type rateLimitsResult struct {
	RateLimits struct {
		Primary *struct {
			UsedPercent float64 `json:"usedPercent"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"primary"`
		Secondary *struct {
			UsedPercent float64 `json:"usedPercent"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"secondary"`
		PlanType string `json:"planType"`
	} `json:"rateLimits"`
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
	stats := CodexStats{
		Available: true,
		PlanType:  rl.PlanType,
		Session:   QuotaInfo{Label: "Session", Percent: -1},
		Weekly:    QuotaInfo{Label: "Weekly", Percent: -1},
	}

	if rl.Primary != nil {
		stats.Session.Percent = int(100 - rl.Primary.UsedPercent)
		if rl.Primary.ResetsAt > 0 {
			stats.Session.ResetsAt = time.Unix(rl.Primary.ResetsAt, 0)
		}
	}
	if rl.Secondary != nil {
		stats.Weekly.Percent = int(100 - rl.Secondary.UsedPercent)
		if rl.Secondary.ResetsAt > 0 {
			stats.Weekly.ResetsAt = time.Unix(rl.Secondary.ResetsAt, 0)
		}
	}

	return stats
}
