package quota

type Stats struct {
	Claude   ClaudeStats
	Codex    CodexStats
	DeepSeek DeepSeekStats
}

// FetchStats loads the current quota state for every tray provider.
func FetchStats() Stats {
	claudeChannel := make(chan ClaudeStats, 1)
	codexChannel := make(chan CodexStats, 1)
	deepSeekChannel := make(chan DeepSeekStats, 1)
	go func() {
		stats := fetchClaude()
		claudeChannel <- stats
	}()
	go func() {
		stats := fetchCodex()
		codexChannel <- stats
	}()
	go func() {
		stats := fetchDeepSeek()
		deepSeekChannel <- stats
	}()
	claude := <-claudeChannel
	codex := <-codexChannel
	deepSeek := <-deepSeekChannel
	stats := Stats{
		Claude:   claude,
		Codex:    codex,
		DeepSeek: deepSeek,
	}
	return stats
}
