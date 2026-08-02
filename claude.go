package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

type QuotaInfo struct {
	Label    string
	Percent  int       // -1 = unknown
	ResetsAt time.Time // zero = unknown
}

type ClaudeStats struct {
	Available bool
	Session   QuotaInfo
	Weekly    QuotaInfo
	Error     string
}

// Endpoint + headers mirror Claude Code's own fetchUtilization
// (GET /api/oauth/usage with the keychain OAuth access token).
const (
	claudeUsageURL     = "https://api.anthropic.com/api/oauth/usage"
	claudeOAuthBeta    = "oauth-2025-04-20"
	claudeKeychainItem = "Claude Code-credentials"
)

type claudeUsageResponse struct {
	FiveHour *claudeWindow `json:"five_hour"`
	SevenDay *claudeWindow `json:"seven_day"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // ms epoch
	} `json:"claudeAiOauth"`
}

func fetchClaude() ClaudeStats {
	token, err := claudeAccessToken()
	if err != nil {
		return ClaudeStats{Error: "claude: " + err.Error()}
	}

	body, err := claudeUsageRequest(token)
	if err != nil {
		return ClaudeStats{Error: "claude: " + err.Error()}
	}

	return parseClaudeUsage(body)
}

// claudeAccessToken reads the OAuth token Claude Code stores in the macOS keychain.
func claudeAccessToken() (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", claudeKeychainItem, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no keychain credentials (run claude /login)")
	}

	var creds claudeCredentials
	if err := json.Unmarshal(out, &creds); err != nil {
		return "", fmt.Errorf("unreadable keychain credentials")
	}

	oauth := creds.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return "", fmt.Errorf("no OAuth token (run claude /login)")
	}
	if oauth.ExpiresAt > 0 && time.Now().After(time.UnixMilli(oauth.ExpiresAt)) {
		return "", fmt.Errorf("OAuth token expired (start claude to refresh)")
	}
	return oauth.AccessToken, nil
}

func claudeUsageRequest(token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", claudeOAuthBeta)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage API returned %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func parseClaudeUsage(body []byte) ClaudeStats {
	var usage claudeUsageResponse
	if err := json.Unmarshal(body, &usage); err != nil {
		return ClaudeStats{Error: "claude: malformed usage response"}
	}

	return ClaudeStats{
		Available: true,
		Session:   claudeQuota("Session", usage.FiveHour),
		Weekly:    claudeQuota("Weekly", usage.SevenDay),
	}
}

// claudeQuota converts the API's utilization (percent used) into remaining percent.
func claudeQuota(label string, w *claudeWindow) QuotaInfo {
	if w == nil {
		return QuotaInfo{Label: label, Percent: -1}
	}

	remaining := 100 - int(w.Utilization)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}

	q := QuotaInfo{Label: label, Percent: remaining}
	if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
		q.ResetsAt = t.Local()
	}
	return q
}
