package quota

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const deepSeekBalanceURL = "https://api.deepseek.com/user/balance"

var errDeepSeekKeyNotFound = errors.New("deepseek key not found")

type DeepSeekBalance struct {
	Currency string `json:"currency"`
	Total    string `json:"total_balance"`
}

type DeepSeekStats struct {
	Available bool
	Balances  []DeepSeekBalance
	Error     string
}

type deepSeekBalanceResponse struct {
	Balances []DeepSeekBalance `json:"balance_infos"`
}

type openCodeAPIAuth struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

func fetchDeepSeek() DeepSeekStats {
	apiKey, err := deepSeekAPIKey()
	if err != nil {
		errorMessage := err.Error()
		return DeepSeekStats{Error: errorMessage}
	}

	timeout := 10 * time.Second
	client := &http.Client{Timeout: timeout}
	stats := fetchDeepSeekBalance(client, deepSeekBalanceURL, apiKey)
	return stats
}

func fetchDeepSeekBalance(client *http.Client, endpoint string, apiKey string) DeepSeekStats {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return DeepSeekStats{Error: "deepseek request error"}
	}

	authorization := "Bearer " + apiKey
	request.Header.Set("Authorization", authorization)

	response, err := client.Do(request)
	if err != nil {
		return DeepSeekStats{Error: "deepseek request failed"}
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized {
		return DeepSeekStats{Error: "deepseek authentication failed"}
	}
	if response.StatusCode != http.StatusOK {
		errorMessage := fmt.Sprintf("deepseek HTTP %d", response.StatusCode)
		return DeepSeekStats{Error: errorMessage}
	}

	var balanceResponse deepSeekBalanceResponse
	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&balanceResponse)
	if err != nil {
		return DeepSeekStats{Error: "deepseek parse error"}
	}
	balanceCount := len(balanceResponse.Balances)
	if balanceCount == 0 {
		return DeepSeekStats{Error: "deepseek balance unavailable"}
	}

	stats := DeepSeekStats{
		Available: true,
		Balances:  balanceResponse.Balances,
	}
	return stats
}

func deepSeekAPIKey() (string, error) {
	environmentKey := os.Getenv("DEEPSEEK_API_KEY")
	if environmentKey != "" {
		return environmentKey, nil
	}

	authContent := os.Getenv("OPENCODE_AUTH_CONTENT")
	if authContent != "" {
		authData := []byte(authContent)
		apiKey := deepSeekKeyFromAuth(authData)
		if apiKey != "" {
			return apiKey, nil
		}
	}

	authPath, err := openCodeAuthPath()
	if err != nil {
		return "", errDeepSeekKeyNotFound
	}
	authData, err := os.ReadFile(authPath)
	if err != nil {
		return "", errDeepSeekKeyNotFound
	}

	apiKey := deepSeekKeyFromAuth(authData)
	if apiKey == "" {
		return "", errDeepSeekKeyNotFound
	}
	return apiKey, nil
}

func openCodeAuthPath() (string, error) {
	dataDirectory := os.Getenv("XDG_DATA_HOME")
	if dataDirectory == "" {
		homeDirectory, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataDirectory = filepath.Join(homeDirectory, ".local", "share")
	}

	authPath := filepath.Join(dataDirectory, "opencode", "auth.json")
	return authPath, nil
}

func deepSeekKeyFromAuth(data []byte) string {
	authByProvider := map[string]openCodeAPIAuth{}
	err := json.Unmarshal(data, &authByProvider)
	if err != nil {
		return ""
	}

	auth, exists := authByProvider["deepseek"]
	if !exists {
		return ""
	}
	if auth.Type != "api" {
		return ""
	}
	return auth.Key
}

// FormatDeepSeekBalances formats balances for the tray menu.
func FormatDeepSeekBalances(balances []DeepSeekBalance) string {
	balanceCount := len(balances)
	parts := make([]string, 0, balanceCount)
	for _, balance := range balances {
		part := balance.Total + " " + balance.Currency
		parts = append(parts, part)
	}

	formattedBalances := strings.Join(parts, "  ·  ")
	title := "   Balance   " + formattedBalances
	return title
}
