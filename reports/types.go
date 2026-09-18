package reports

import "time"

const (
	usageDateLayout = "2006-01-02"
	aiClaude        = "Claude"
	aiOpenAI        = "OpenAI"
	aiDeepSeek      = "DeepSeek"
	aiOther         = "Other"
	costReported    = "reported"
	costComputed    = "computed"
	costEstimated   = "estimated"
	costMissing     = "missing"
	costMixed       = "mixed"
)

type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

func (tokens TokenUsage) Total() int64 {
	total := tokens.Input + tokens.Output + tokens.Reasoning + tokens.CacheRead + tokens.CacheWrite
	return total
}

type DailyModelUsage struct {
	Date        string     `json:"date"`
	Source      string     `json:"-"`
	AI          string     `json:"ai"`
	Provider    string     `json:"provider"`
	Model       string     `json:"model"`
	Tokens      TokenUsage `json:"tokens"`
	TotalTokens int64      `json:"total_tokens"`
	CostUSD     float64    `json:"cost_usd"`
	CostKind    string     `json:"cost_kind"`
}

type ModelMonthSummary struct {
	AI       string  `json:"ai"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
	CostKind string  `json:"cost_kind"`
}

type PeriodSummary struct {
	Key          string              `json:"key"`
	Label        string              `json:"label"`
	TotalTokens  int64               `json:"total_tokens"`
	TotalCostUSD float64             `json:"total_cost_usd"`
	Models       []ModelMonthSummary `json:"models"`
}

type UsageSnapshot struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	CurrentMonth  PeriodSummary     `json:"current_month"`
	PreviousMonth PeriodSummary     `json:"previous_month"`
	Rows          []DailyModelUsage `json:"rows"`
	Warnings      []string          `json:"warnings"`
}

func newDailyModelUsage(date string, source string, provider string, model string, tokens TokenUsage, cost float64, costKind string) DailyModelUsage {
	totalTokens := tokens.Total()
	ai := usageAI(source, provider)
	row := DailyModelUsage{
		Date:        date,
		Source:      source,
		AI:          ai,
		Provider:    provider,
		Model:       model,
		Tokens:      tokens,
		TotalTokens: totalTokens,
		CostUSD:     cost,
		CostKind:    costKind,
	}
	return row
}
