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

const codexUsageSource = "Codex"

type codexRawUsage struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
}

type codexUsageInfo struct {
	Last  *codexRawUsage `json:"last_token_usage"`
	Total *codexRawUsage `json:"total_token_usage"`
}

type codexUsagePayload struct {
	Type          string          `json:"type"`
	TurnID        string          `json:"turn_id"`
	Model         string          `json:"model"`
	ModelProvider string          `json:"model_provider"`
	Info          *codexUsageInfo `json:"info"`
}

type codexUsageLine struct {
	Timestamp string            `json:"timestamp"`
	Type      string            `json:"type"`
	Payload   codexUsagePayload `json:"payload"`
}

type codexTurn struct {
	Model    string
	Provider string
}

func syncCodexUsage(ctx context.Context, cache *usageCache, startTime time.Time) []string {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return []string{"Codex: home directory unavailable"}
	}
	sessionsRoot := filepath.Join(homeDirectory, ".codex", "sessions")
	source := usageFileSource{
		Name:  codexUsageSource,
		Root:  sessionsRoot,
		Parse: parseCodexUsageFile,
	}
	warnings := syncUsageFiles(ctx, cache, startTime, source)
	return warnings
}

func parseCodexUsageFile(ctx context.Context, path string, startTime time.Time) ([]DailyModelUsage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rowsByKey := map[usageAggregateKey]DailyModelUsage{}
	turns := map[string]codexTurn{}
	currentTurn := codexTurn{Provider: "openai"}
	previousTotal := codexRawUsage{}
	seenTotals := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	maximumLineSize := 16 * 1024 * 1024
	scannerBuffer := make([]byte, 64*1024)
	scanner.Buffer(scannerBuffer, maximumLineSize)
	for scanner.Scan() {
		contextError := ctx.Err()
		if contextError != nil {
			return nil, contextError
		}
		lineData := scanner.Bytes()
		var line codexUsageLine
		unmarshalError := json.Unmarshal(lineData, &line)
		if unmarshalError != nil {
			continue
		}
		if line.Type == "turn_context" {
			currentTurn = updateCodexTurn(currentTurn, line.Payload)
			if line.Payload.TurnID != "" {
				turns[line.Payload.TurnID] = currentTurn
			}
			continue
		}
		if line.Type != "event_msg" {
			continue
		}
		if line.Payload.Type != "token_count" {
			continue
		}
		if line.Payload.Info == nil {
			continue
		}
		timestamp, parseError := time.Parse(time.RFC3339Nano, line.Timestamp)
		if parseError != nil {
			continue
		}
		rawUsage, nextTotal, accepted := codexEventUsage(*line.Payload.Info, previousTotal, seenTotals)
		if !accepted {
			continue
		}
		previousTotal = nextTotal
		isBeforeStart := timestamp.Before(startTime)
		if isBeforeStart {
			continue
		}
		turn := currentTurn
		storedTurn, foundTurn := turns[line.Payload.TurnID]
		if foundTurn {
			turn = storedTurn
		}
		if turn.Model == "" {
			continue
		}
		tokens := normalizeCodexTokens(rawUsage)
		totalTokens := tokens.Total()
		if totalTokens == 0 {
			continue
		}
		localTime := timestamp.Local()
		day := localTime.Format(usageDateLayout)
		costKind := costEstimated
		cost := float64(0)
		rate, foundRate := codexUsageRate(turn.Model)
		if foundRate {
			cost = estimateUsageCost(tokens, rate)
		} else {
			costKind = costMissing
		}
		row := newDailyModelUsage(day, codexUsageSource, turn.Provider, turn.Model, tokens, cost, costKind)
		addUsageRow(rowsByKey, row)
	}
	scanError := scanner.Err()
	if scanError != nil {
		return nil, scanError
	}
	rows := usageRowsFromMap(rowsByKey)
	return rows, nil
}

func updateCodexTurn(current codexTurn, payload codexUsagePayload) codexTurn {
	updated := current
	if payload.Model != "" {
		updated.Model = payload.Model
	}
	if payload.ModelProvider != "" {
		updated.Provider = payload.ModelProvider
	}
	if updated.Provider == "" {
		updated.Provider = "openai"
	}
	return updated
}

func codexEventUsage(info codexUsageInfo, previous codexRawUsage, seen map[string]struct{}) (codexRawUsage, codexRawUsage, bool) {
	if info.Total != nil {
		current := *info.Total
		identity := codexUsageIdentity(current)
		_, alreadySeen := seen[identity]
		if alreadySeen {
			return codexRawUsage{}, previous, false
		}
		seen[identity] = struct{}{}
		delta := subtractCodexUsage(current, previous)
		return delta, current, true
	}
	if info.Last != nil {
		last := *info.Last
		next := addCodexRawUsage(previous, last)
		return last, next, true
	}
	return codexRawUsage{}, previous, false
}

func codexUsageIdentity(usage codexRawUsage) string {
	input := strconv.FormatInt(usage.Input, 10)
	cached := strconv.FormatInt(usage.Cached, 10)
	cacheWrite := strconv.FormatInt(usage.CacheWrite, 10)
	output := strconv.FormatInt(usage.Output, 10)
	reasoning := strconv.FormatInt(usage.Reasoning, 10)
	identity := input + ":" + cached + ":" + cacheWrite + ":" + output + ":" + reasoning
	return identity
}

func subtractCodexUsage(current codexRawUsage, previous codexRawUsage) codexRawUsage {
	usage := codexRawUsage{
		Input:      positiveDifference(current.Input, previous.Input),
		Cached:     positiveDifference(current.Cached, previous.Cached),
		CacheWrite: positiveDifference(current.CacheWrite, previous.CacheWrite),
		Output:     positiveDifference(current.Output, previous.Output),
		Reasoning:  positiveDifference(current.Reasoning, previous.Reasoning),
	}
	return usage
}

func addCodexRawUsage(left codexRawUsage, right codexRawUsage) codexRawUsage {
	usage := codexRawUsage{
		Input:      left.Input + right.Input,
		Cached:     left.Cached + right.Cached,
		CacheWrite: left.CacheWrite + right.CacheWrite,
		Output:     left.Output + right.Output,
		Reasoning:  left.Reasoning + right.Reasoning,
	}
	return usage
}

func positiveDifference(current int64, previous int64) int64 {
	difference := current - previous
	if difference < 0 {
		return current
	}
	return difference
}

func normalizeCodexTokens(raw codexRawUsage) TokenUsage {
	uncachedInput := raw.Input - raw.Cached
	if uncachedInput < 0 {
		uncachedInput = 0
	}
	nonReasoningOutput := raw.Output - raw.Reasoning
	if nonReasoningOutput < 0 {
		nonReasoningOutput = 0
	}
	tokens := TokenUsage{
		Input:      uncachedInput,
		Output:     nonReasoningOutput,
		Reasoning:  raw.Reasoning,
		CacheRead:  raw.Cached,
		CacheWrite: raw.CacheWrite,
	}
	return tokens
}
