package reports

import (
	"strings"
	"testing"
	"time"
)

func TestRenderUsageReportEmbedsSnapshot(t *testing.T) {
	snapshot := UsageSnapshot{
		GeneratedAt: time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC),
		Rows:        make([]DailyModelUsage, 0),
		Warnings:    make([]string, 0),
	}
	reportHTML, err := renderUsageReport(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	placeholderRemains := strings.Contains(reportHTML, usageSnapshotPlaceholder)
	if placeholderRemains {
		t.Fatal("snapshot placeholder remains in rendered report")
	}
	generatedAtJSON := `"generated_at":"2026-09-13T10:00:00Z"`
	containsGeneratedAt := strings.Contains(reportHTML, generatedAtJSON)
	if !containsGeneratedAt {
		t.Fatal("rendered report does not contain snapshot JSON")
	}
	containsQuotaBurn := strings.Contains(reportHTML, "quota_burn")
	if containsQuotaBurn {
		t.Fatal("rendered report contains quota burn data")
	}
}
