package reports

import "strings"

type usageRate struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

var claudeUsageRates = map[string]usageRate{
	"claude-haiku-4-5-20251001": {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25},
	"claude-opus-4-8":           {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-5":             {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-sonnet-4-6":         {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-sonnet-5":           {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
}

var codexUsageRates = map[string]usageRate{
	"gpt-5.4-mini":  {Input: 0.75, Output: 4.5, CacheRead: 0.075},
	"gpt-5.5":       {Input: 5, Output: 30, CacheRead: 0.5},
	"gpt-5.6-luna":  {Input: 0.2, Output: 1.2, CacheRead: 0.02, CacheWrite: 0.25},
	"gpt-5.6-sol":   {Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5},
	"gpt-5.6-terra": {Input: 2, Output: 12, CacheRead: 0.2, CacheWrite: 2.5},
	"gpt-6-astra":   {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5},
}

func estimateUsageCost(tokens TokenUsage, rate usageRate) float64 {
	inputCost := float64(tokens.Input) * rate.Input
	outputTokens := tokens.Output + tokens.Reasoning
	outputCost := float64(outputTokens) * rate.Output
	cacheReadCost := float64(tokens.CacheRead) * rate.CacheRead
	cacheWriteCost := float64(tokens.CacheWrite) * rate.CacheWrite
	totalPerMillion := inputCost + outputCost + cacheReadCost + cacheWriteCost
	cost := totalPerMillion / 1_000_000
	return cost
}

func claudeUsageRate(model string) (usageRate, bool) {
	rate, found := claudeUsageRates[model]
	if found {
		return rate, true
	}
	isOpusFive := strings.HasPrefix(model, "claude-opus-5")
	if isOpusFive {
		rate = claudeUsageRates["claude-opus-5"]
		return rate, true
	}
	isSonnetFive := strings.HasPrefix(model, "claude-sonnet-5")
	if isSonnetFive {
		rate = claudeUsageRates["claude-sonnet-5"]
		return rate, true
	}
	return usageRate{}, false
}

func codexUsageRate(model string) (usageRate, bool) {
	rate, found := codexUsageRates[model]
	return rate, found
}
