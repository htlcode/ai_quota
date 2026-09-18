package reports

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const claudeUsageSource = "Claude Code"

type claudeUsageCounters struct {
	Input         int64  `json:"input_tokens"`
	Output        int64  `json:"output_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
	CacheCreation *int64 `json:"cache_creation_input_tokens"`
}

type claudeUsageMessage struct {
	ID      string              `json:"id"`
	Role    string              `json:"role"`
	Model   string              `json:"model"`
	Usage   claudeUsageCounters `json:"usage"`
	CostUSD *float64            `json:"costUSD"`
}

type claudeUsageLine struct {
	Type      string             `json:"type"`
	UUID      string             `json:"uuid"`
	RequestID string             `json:"requestId"`
	SessionID string             `json:"sessionId"`
	Timestamp string             `json:"timestamp"`
	CostUSD   *float64           `json:"costUSD"`
	Message   claudeUsageMessage `json:"message"`
}

type claudeRequestUsage struct {
	Timestamp time.Time
	Model     string
	Tokens    TokenUsage
	CostUSD   *float64
}

func syncClaudeUsage(ctx context.Context, cache *usageCache, startTime time.Time) []string {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return []string{"Claude Code: home directory unavailable"}
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(homeDirectory, ".claude")
	}
	projectsRoot := filepath.Join(claudeHome, "projects")
	skipDirectories := map[string]struct{}{
		"debug":        {},
		"tool-results": {},
	}
	source := usageFileSource{
		Name:            claudeUsageSource,
		Root:            projectsRoot,
		SkipDirectories: skipDirectories,
		Parse:           parseClaudeUsageFile,
	}
	warnings := syncUsageFiles(ctx, cache, startTime, source)
	return warnings
}

func parseClaudeUsageFile(ctx context.Context, path string, startTime time.Time) ([]DailyModelUsage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	requests := map[string]claudeRequestUsage{}
	scanner := bufio.NewScanner(file)
	maximumLineSize := 16 * 1024 * 1024
	scannerBuffer := make([]byte, 64*1024)
	scanner.Buffer(scannerBuffer, maximumLineSize)

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		contextError := ctx.Err()
		if contextError != nil {
			return nil, contextError
		}
		lineData := scanner.Bytes()
		var line claudeUsageLine
		err = json.Unmarshal(lineData, &line)
		if err != nil {
			continue
		}
		if line.Type != "assistant" {
			continue
		}
		if line.Message.Role != "assistant" {
			continue
		}
		if line.Message.Model == "" {
			continue
		}
		timestamp, parseError := time.Parse(time.RFC3339Nano, line.Timestamp)
		if parseError != nil {
			continue
		}
		isBeforeStart := timestamp.Before(startTime)
		if isBeforeStart {
			continue
		}
		tokens := claudeTokens(line.Message.Usage)
		totalTokens := tokens.Total()
		if totalTokens == 0 {
			continue
		}

		requestKey := claudeRequestKey(line, lineNumber)
		reportedCost := line.CostUSD
		if reportedCost == nil {
			reportedCost = line.Message.CostUSD
		}
		request := claudeRequestUsage{
			Timestamp: timestamp,
			Model:     line.Message.Model,
			Tokens:    tokens,
			CostUSD:   reportedCost,
		}
		requests[requestKey] = request
	}
	err = scanner.Err()
	if err != nil {
		return nil, err
	}

	rows := aggregateClaudeRequests(requests)
	return rows, nil
}

func claudeTokens(usage claudeUsageCounters) TokenUsage {
	cacheRead := int64(0)
	if usage.CacheRead != nil {
		cacheRead = *usage.CacheRead
	}
	cacheWrite := int64(0)
	if usage.CacheCreation != nil {
		cacheWrite = *usage.CacheCreation
	}
	tokens := TokenUsage{
		Input:      usage.Input,
		Output:     usage.Output,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
	}
	return tokens
}

func claudeRequestKey(line claudeUsageLine, lineNumber int) string {
	identity := line.RequestID
	if identity == "" {
		identity = line.Message.ID
	}
	if identity == "" {
		identity = line.UUID
	}
	if identity == "" {
		lineText := strconv.Itoa(lineNumber)
		identity = line.Timestamp + ":" + lineText
	}
	key := line.SessionID + ":" + identity
	return key
}

func aggregateClaudeRequests(requests map[string]claudeRequestUsage) []DailyModelUsage {
	rowsByKey := map[usageAggregateKey]DailyModelUsage{}
	for _, request := range requests {
		localTime := request.Timestamp.Local()
		day := localTime.Format(usageDateLayout)
		costKind := costComputed
		cost := float64(0)
		if request.CostUSD != nil {
			cost = *request.CostUSD
			costKind = costReported
		} else {
			rate, found := claudeUsageRate(request.Model)
			if found {
				cost = estimateUsageCost(request.Tokens, rate)
			} else {
				costKind = costMissing
			}
		}
		row := newDailyModelUsage(day, claudeUsageSource, "anthropic", request.Model, request.Tokens, cost, costKind)
		addUsageRow(rowsByKey, row)
	}
	rows := usageRowsFromMap(rowsByKey)
	return rows
}
