package quota

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(request *http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := roundTrip(request)
	return response, err
}

func TestFetchDeepSeekBalance(t *testing.T) {
	transportFunction := func(request *http.Request) (*http.Response, error) {
		authorization := request.Header.Get("Authorization")
		if authorization != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", authorization)
		}

		responseJSON := `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"4.82","granted_balance":"0.00","topped_up_balance":"4.82"}]}`
		responseReader := strings.NewReader(responseJSON)
		responseBody := io.NopCloser(responseReader)
		response := &http.Response{
			StatusCode: http.StatusOK,
			Body:       responseBody,
		}
		return response, nil
	}
	transport := roundTripFunc(transportFunction)
	client := &http.Client{Transport: transport}
	stats := fetchDeepSeekBalance(client, deepSeekBalanceURL, "test-key")
	if !stats.Available {
		t.Fatalf("DeepSeek unavailable: %s", stats.Error)
	}
	balanceCount := len(stats.Balances)
	if balanceCount != 1 {
		t.Fatalf("balance count = %d, want 1", balanceCount)
	}

	balance := stats.Balances[0]
	if balance.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", balance.Currency)
	}
	if balance.Total != "4.82" {
		t.Fatalf("total balance = %q, want 4.82", balance.Total)
	}
}

func TestFetchDeepSeekBalanceRejectsUnauthorizedKey(t *testing.T) {
	transportFunction := func(request *http.Request) (*http.Response, error) {
		responseBody := http.NoBody
		response := &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       responseBody,
		}
		return response, nil
	}
	transport := roundTripFunc(transportFunction)
	client := &http.Client{Transport: transport}
	stats := fetchDeepSeekBalance(client, deepSeekBalanceURL, "invalid-key")
	if stats.Error != "deepseek authentication failed" {
		t.Fatalf("error = %q, want authentication failure", stats.Error)
	}
}

func TestDeepSeekKeyFromOpenCodeAuth(t *testing.T) {
	authData := []byte(`{"deepseek":{"type":"api","key":"stored-key"}}`)
	apiKey := deepSeekKeyFromAuth(authData)
	if apiKey != "stored-key" {
		t.Fatalf("API key = %q, want stored-key", apiKey)
	}
}

func TestDeepSeekAPIKeyUsesEnvironment(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "environment-key")
	t.Setenv("OPENCODE_AUTH_CONTENT", "")

	apiKey, err := deepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "environment-key" {
		t.Fatalf("API key = %q, want environment-key", apiKey)
	}
}

func TestDeepSeekAPIKeyUsesOpenCodeAuthContent(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	authContent := `{"deepseek":{"type":"api","key":"content-key"}}`
	t.Setenv("OPENCODE_AUTH_CONTENT", authContent)

	apiKey, err := deepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "content-key" {
		t.Fatalf("API key = %q, want content-key", apiKey)
	}
}

func TestFormatDeepSeekBalances(t *testing.T) {
	balances := []DeepSeekBalance{
		{Currency: "USD", Total: "4.82"},
		{Currency: "CNY", Total: "12.00"},
	}
	title := FormatDeepSeekBalances(balances)
	want := "   Balance   4.82 USD  ·  12.00 CNY"
	if title != want {
		t.Fatalf("title = %q, want %q", title, want)
	}
}
