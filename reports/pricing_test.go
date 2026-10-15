package reports

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	err := LoadPricing("../pricing.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.Exit(code)
}

func preserveTestPricing(t *testing.T) {
	t.Helper()
	claudeRates := claudeUsageRates
	codexRates := codexUsageRates
	t.Cleanup(func() {
		claudeUsageRates = claudeRates
		codexUsageRates = codexRates
	})
}

func writeTestPricing(t *testing.T, content string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "pricing.json")
	data := []byte(content)
	err := os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPricingUsesExternalRates(t *testing.T) {
	preserveTestPricing(t)
	before := usagePricingFingerprint()
	content := `{"claude":{"custom-claude":{"input":7,"output":11,"cache_read":0.5,"cache_write":9,"cache_write_long":14}},"codex":{"custom-codex":{"input":3}}}`
	path := writeTestPricing(t, content)
	err := LoadPricing(path)
	if err != nil {
		t.Fatal(err)
	}
	rate, found := claudeUsageRate("custom-claude")
	if !found {
		t.Fatal("external Claude model is missing")
	}
	want := usageRate{Input: 7, Output: 11, CacheRead: 0.5, CacheWrite: 9, CacheWriteLong: 14}
	if rate != want {
		t.Errorf("Claude rate = %+v, want %+v", rate, want)
	}
	rate, found = codexUsageRate("custom-codex")
	if !found {
		t.Fatal("external Codex model is missing")
	}
	want = usageRate{Input: 3}
	if rate != want {
		t.Errorf("Codex rate = %+v, want %+v", rate, want)
	}
	_, found = codexUsageRate("gpt-5.5")
	if found {
		t.Error("removed model still has a rate")
	}
	_, found = claudeUsageRate("claude-opus-5-future")
	if found {
		t.Error("missing Claude family must remain unpriced")
	}
	after := usagePricingFingerprint()
	if after == before {
		t.Error("changed rates must invalidate the cache fingerprint")
	}
	err = LoadPricing(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := usagePricingFingerprint()
	if reloaded != after {
		t.Error("unchanged rates must preserve the cache fingerprint")
	}
}

func TestLoadPricingRejectsInvalidFiles(t *testing.T) {
	preserveTestPricing(t)
	before := usagePricingFingerprint()
	cases := map[string]string{
		"malformed":       `{`,
		"null":            `null`,
		"missing Claude":  `{"codex":{"model":{}}}`,
		"empty Codex":     `{"claude":{"model":{}},"codex":{}}`,
		"negative rate":   `{"claude":{"model":{"cache_write_long":-1}},"codex":{"model":{}}}`,
		"unknown field":   `{"claude":{"model":{"inpt":1}},"codex":{"model":{}}}`,
		"empty model":     `{"claude":{"":{}},"codex":{"model":{}}}`,
		"trailing object": `{"claude":{"model":{}},"codex":{"model":{}}} {}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeTestPricing(t, content)
			err := LoadPricing(path)
			if err == nil {
				t.Fatal("invalid pricing was accepted")
			}
			after := usagePricingFingerprint()
			if after != before {
				t.Error("failed load changed active rates")
			}
		})
	}
}

func TestLoadPricingRejectsMissingFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "missing.json")
	err := LoadPricing(path)
	if err == nil {
		t.Fatal("missing pricing file was accepted")
	}
}
