package reports

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Bump when parsing or pricing logic changes so cached file costs are recomputed.
const usagePricingVersion = "2"

type usageRate struct {
	Input          float64 `json:"input"`
	Output         float64 `json:"output"`
	CacheRead      float64 `json:"cache_read"`
	CacheWrite     float64 `json:"cache_write"`
	CacheWriteLong float64 `json:"cache_write_long"`
}

var claudeUsageRates map[string]usageRate

var codexUsageRates map[string]usageRate

type usagePricing struct {
	Claude map[string]usageRate `json:"claude"`
	Codex  map[string]usageRate `json:"codex"`
}

// LoadPricing loads USD rates per million tokens before reports are used.
func LoadPricing(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open pricing file: %w", err)
	}
	defer file.Close()

	var pricing usagePricing
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&pricing)
	if err != nil {
		return fmt.Errorf("decode pricing file %s: %w", path, err)
	}
	var trailing any
	err = decoder.Decode(&trailing)
	if err != io.EOF {
		return fmt.Errorf("pricing file %s must contain one JSON object", path)
	}
	err = validateUsageRates("claude", pricing.Claude)
	if err != nil {
		return err
	}
	err = validateUsageRates("codex", pricing.Codex)
	if err != nil {
		return err
	}
	claudeUsageRates = pricing.Claude
	codexUsageRates = pricing.Codex
	return nil
}

func validateUsageRates(provider string, rates map[string]usageRate) error {
	rateCount := len(rates)
	if rateCount == 0 {
		return fmt.Errorf("pricing: %s must contain at least one model", provider)
	}
	for model, rate := range rates {
		if model == "" {
			return errors.New("pricing: model names must not be empty")
		}
		values := []float64{rate.Input, rate.Output, rate.CacheRead, rate.CacheWrite, rate.CacheWriteLong}
		for _, value := range values {
			if value < 0 {
				return fmt.Errorf("pricing: %s/%s rates must not be negative", provider, model)
			}
		}
	}
	return nil
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

// usagePricingFingerprint changes whenever a rate or the pricing version changes.
func usagePricingFingerprint() string {
	rates := fmt.Sprint(usagePricingVersion, claudeUsageRates, codexUsageRates)
	ratesData := []byte(rates)
	digest := sha256.Sum256(ratesData)
	fingerprint := hex.EncodeToString(digest[:])
	return fingerprint
}

func claudeUsageRate(model string) (usageRate, bool) {
	rate, found := claudeUsageRates[model]
	if found {
		return rate, true
	}
	isOpusFive := strings.HasPrefix(model, "claude-opus-5")
	if isOpusFive {
		rate, found = claudeUsageRates["claude-opus-5"]
		return rate, found
	}
	isSonnetFive := strings.HasPrefix(model, "claude-sonnet-5")
	if isSonnetFive {
		rate, found = claudeUsageRates["claude-sonnet-5"]
		return rate, found
	}
	return usageRate{}, false
}

func codexUsageRate(model string) (usageRate, bool) {
	rate, found := codexUsageRates[model]
	return rate, found
}
