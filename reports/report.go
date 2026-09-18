package reports

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const usageSnapshotPlaceholder = "__USAGE_SNAPSHOT__"
const usageReportFilename = "ai-quota-usage-report.html"

//go:embed report.html
var usageReportTemplate string

// Open creates and displays the current usage report.
func Open() error {
	background := context.Background()
	timeout := 90 * time.Second
	ctx, cancel := context.WithTimeout(background, timeout)
	defer cancel()

	snapshot, err := loadUsageSnapshot(ctx)
	if err != nil {
		return err
	}
	reportHTML, err := renderUsageReport(snapshot)
	if err != nil {
		return err
	}
	temporaryDirectory := os.TempDir()
	reportPath := filepath.Join(temporaryDirectory, usageReportFilename)
	reportBytes := []byte(reportHTML)
	err = os.WriteFile(reportPath, reportBytes, 0o600)
	if err != nil {
		return err
	}

	command := exec.Command("/usr/bin/open", reportPath)
	err = command.Run()
	return err
}

func renderUsageReport(snapshot UsageSnapshot) (string, error) {
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	placeholderExists := strings.Contains(usageReportTemplate, usageSnapshotPlaceholder)
	if !placeholderExists {
		return "", errors.New("usage report snapshot placeholder is missing")
	}
	serializedSnapshot := string(snapshotJSON)
	reportHTML := strings.Replace(usageReportTemplate, usageSnapshotPlaceholder, serializedSnapshot, 1)
	return reportHTML, nil
}
