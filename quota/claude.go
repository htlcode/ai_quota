package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

type QuotaInfo struct {
	Label    string
	Percent  int       // -1 = unknown
	ResetsAt time.Time // zero = unknown
}

type ClaudeStats struct {
	Available bool
	Session   QuotaInfo
	Weekly    QuotaInfo
	Error     string
}

var rePct = regexp.MustCompile(`(?i)(\d{1,3})\s*%\s*(used|left)`)
var reClaudeSection = regexp.MustCompile(`(?i)^\s*Current\s+(session|week)(?:\s*\(([^)]+)\))?`)

// fetchClaude prefers the direct API and uses CLI sources only as fallbacks.
func fetchClaude() ClaudeStats {
	stats := fetchClaudeWithSources(fetchClaudeAPI, fetchClaudePTY, fetchClaudeCachedUsage)
	return stats
}

func fetchClaudeWithSources(fetchAPI func() ClaudeStats, fetchPTY func() ClaudeStats, fetchCache func() ClaudeStats) ClaudeStats {
	apiStats := fetchAPI()
	if claudeStatsComplete(apiStats) {
		return apiStats
	}

	ptyStats := fetchPTY()
	stats := mergeClaudeStats(apiStats, ptyStats)
	if claudeStatsComplete(stats) {
		return stats
	}

	if apiStats.Available == false {
		retriedAPIStats := fetchAPI()
		stats = mergeClaudeStats(retriedAPIStats, stats)
		if claudeStatsComplete(stats) {
			return stats
		}
	}

	cachedStats := fetchCache()
	stats = mergeClaudeStats(stats, cachedStats)
	if claudeStatsHasQuota(stats) {
		return stats
	}

	errorMessage := fmt.Sprintf("claude: pty (%s); cache (%s); api (%s)", ptyStats.Error, cachedStats.Error, apiStats.Error)
	stats = ClaudeStats{Error: errorMessage}
	return stats
}

func mergeClaudeStats(primary ClaudeStats, fallback ClaudeStats) ClaudeStats {
	if !primary.Available {
		return fallback
	}
	if !fallback.Available {
		return primary
	}

	session := mergeClaudeQuota(primary.Session, fallback.Session)
	weekly := mergeClaudeQuota(primary.Weekly, fallback.Weekly)
	stats := ClaudeStats{
		Available: true,
		Session:   session,
		Weekly:    weekly,
	}
	return stats
}

func mergeClaudeQuota(primary QuotaInfo, fallback QuotaInfo) QuotaInfo {
	quota := primary
	if quota.Percent < 0 {
		quota.Percent = fallback.Percent
	}
	if quota.ResetsAt.IsZero() {
		quota.ResetsAt = fallback.ResetsAt
	}
	return quota
}

func claudeStatsComplete(stats ClaudeStats) bool {
	if !stats.Available {
		return false
	}
	if stats.Session.Percent < 0 {
		return false
	}
	if stats.Weekly.Percent < 0 {
		return false
	}
	if stats.Session.ResetsAt.IsZero() {
		return false
	}
	if stats.Weekly.ResetsAt.IsZero() {
		return false
	}
	return true
}

func claudeStatsHasQuota(stats ClaudeStats) bool {
	if !stats.Available {
		return false
	}
	if stats.Session.Percent >= 0 {
		return true
	}
	if stats.Weekly.Percent >= 0 {
		return true
	}
	return false
}

const claudeUsageColumns = 160
const claudeUsageRows = 50
const claudeUsageTimeout = 20 * time.Second
const claudeUsagePollInterval = 400 * time.Millisecond
const claudeUsageNudgeDelay = 3 * time.Second
const claudeUsageSettleDelay = 6 * time.Second

// ptyOutputBuffer collects terminal output while the screen is polled.
type ptyOutputBuffer struct {
	mutex sync.Mutex
	data  []byte
}

func (buffer *ptyOutputBuffer) Write(chunk []byte) (int, error) {
	buffer.mutex.Lock()
	buffer.data = append(buffer.data, chunk...)
	buffer.mutex.Unlock()
	written := len(chunk)
	return written, nil
}

func (buffer *ptyOutputBuffer) snapshot() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	text := string(buffer.data)
	return text
}

func fetchClaudePTY() ClaudeStats {
	stats, err := runClaudeUsageInPTY(claudeUsageTimeout)
	if err != nil {
		errorMessage := "claude: " + err.Error()
		failedStats := ClaudeStats{Error: errorMessage}
		return failedStats
	}
	return stats
}

func runClaudeUsageInPTY(timeout time.Duration) (ClaudeStats, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return ClaudeStats{}, fmt.Errorf("claude CLI not found")
	}

	cmd := exec.Command(path, "/usage", "--allowed-tools", "")
	cmd.Env = claudeUsageEnvironment()

	// Setsid: new session/group leader. Works cleanly with PTY (Setpgid
	// would conflict with PTY's controlling-terminal setup on macOS).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	winsize := &pty.Winsize{Rows: claudeUsageRows, Cols: claudeUsageColumns}
	ptmx, err := pty.StartWithSize(cmd, winsize)
	if err != nil {
		return ClaudeStats{}, err
	}
	defer ptmx.Close()

	// Guarantee reap on every return path.
	defer func() {
		killProcessGroup(cmd)
		_ = cmd.Wait()
	}()

	output := &ptyOutputBuffer{}
	doneRead := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, ptmx)
		close(doneRead)
	}()

	stats := waitForClaudeUsageScreen(ptmx, output, doneRead, timeout)
	return stats, nil
}

// Strip CLAUDE_CODE_OAUTH_TOKEN (inference-only, blocks quota access).
func claudeUsageEnvironment() []string {
	environment := os.Environ()
	capacity := len(environment) + 3
	filtered := make([]string, 0, capacity)
	for _, entry := range environment {
		isOAuthToken := strings.HasPrefix(entry, "CLAUDE_CODE_OAUTH_TOKEN=")
		if isOAuthToken {
			continue
		}
		filtered = append(filtered, entry)
	}
	columnsText := strconv.Itoa(claudeUsageColumns)
	rowsText := strconv.Itoa(claudeUsageRows)
	columnsEntry := "COLUMNS=" + columnsText
	rowsEntry := "LINES=" + rowsText
	filtered = append(filtered, "TERM=xterm-256color", columnsEntry, rowsEntry)
	return filtered
}

// The usage screen paints progressively, so the last screen is read only once
// two consecutive renders agree; a partial render is kept as a fallback.
func waitForClaudeUsageScreen(terminal io.Writer, output *ptyOutputBuffer, doneRead <-chan struct{}, timeout time.Duration) ClaudeStats {
	startTime := time.Now()
	deadline := startTime.Add(timeout)
	nudgeTime := startTime.Add(claudeUsageNudgeDelay)
	settleTime := startTime.Add(claudeUsageSettleDelay)
	nudgeSent := false
	bestStats := ClaudeStats{Error: "could not parse /usage output"}
	previousStats := ClaudeStats{}

	for {
		streamClosed := claudeStreamClosed(doneRead)
		raw := output.snapshot()
		rendered := renderVT100(raw, claudeUsageColumns, claudeUsageRows)
		stats := parseClaudeUsagePTY(rendered)
		isStable := claudeQuotasEqual(stats, previousStats)
		isComplete := claudeStatsComplete(stats)
		if isComplete && isStable {
			return stats
		}
		if stats.Available {
			bestStats = stats
		}
		previousStats = stats

		now := time.Now()
		// Reset lines are sometimes overwritten by the redrawing screen; both
		// percentages are enough once they have settled, the API fills the rest.
		hasPercents := claudeStatsHasPercents(stats)
		isSettled := now.After(settleTime)
		if hasPercents && isStable && isSettled {
			return stats
		}
		if streamClosed {
			return bestStats
		}
		isExpired := now.After(deadline)
		if isExpired {
			return bestStats
		}
		shouldNudge := now.After(nudgeTime)
		if !nudgeSent && shouldNudge {
			// Dismiss a prompt that would otherwise hold the usage screen back.
			_, _ = terminal.Write([]byte("\r"))
			nudgeSent = true
		}
		time.Sleep(claudeUsagePollInterval)
	}
}

func claudeStreamClosed(doneRead <-chan struct{}) bool {
	select {
	case <-doneRead:
		return true
	default:
		return false
	}
}

func claudeStatsHasPercents(stats ClaudeStats) bool {
	if !stats.Available {
		return false
	}
	if stats.Session.Percent < 0 {
		return false
	}
	if stats.Weekly.Percent < 0 {
		return false
	}
	return true
}

func claudeQuotasEqual(left ClaudeStats, right ClaudeStats) bool {
	if left.Session.Percent != right.Session.Percent {
		return false
	}
	if left.Weekly.Percent != right.Weekly.Percent {
		return false
	}
	return true
}

// killProcessGroup signals the whole process group. Ignores ESRCH/EPERM
// (process already gone) — only real surprises bubble up.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}

// renderVT100 feeds raw terminal output into a vt100 emulator and dumps the final screen.
func renderVT100(raw string, cols, rows int) string {
	term := vt10x.New(vt10x.WithSize(cols, rows))
	term.Write([]byte(raw))

	var sb strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			ch := term.Cell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(ch)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func parseClaudeUsagePTY(text string) ClaudeStats {
	stats := ClaudeStats{
		Available: true,
		Session:   QuotaInfo{Label: "Session", Percent: -1},
		Weekly:    QuotaInfo{Label: "Weekly", Percent: -1},
	}

	lines := strings.Split(text, "\n")
	found := false

	for i, line := range lines {
		m := reClaudeSection.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		section := claudeSectionLines(lines, i)
		if pct := findPct(section); pct >= 0 {
			label := claudeQuotaLabel(m[1], m[2])
			resetTime := claudeSectionReset(section)
			q := QuotaInfo{
				Label:    label,
				Percent:  pct,
				ResetsAt: resetTime,
			}

			kind := strings.ToLower(m[1])
			model := strings.ToLower(strings.TrimSpace(m[2]))
			if kind == "session" {
				stats.Session = q
				found = true
				continue
			}

			if model == "" || model == "all models" {
				q.Label = "Weekly"
				stats.Weekly = q
				found = true
				continue
			}

			// Ignore per-model weekly sections such as "Current week (Fable)".
			// The menu only displays Claude's aggregate "all models" quota.
		}
	}

	if !found {
		stats.Available = false
		stats.Error = "could not parse /usage output"
	}

	return stats
}

func claudeSectionLines(lines []string, idx int) []string {
	end := len(lines)
	for i := idx + 1; i < len(lines); i++ {
		if reClaudeSection.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return lines[idx:end]
}

func claudeQuotaLabel(kind, model string) string {
	model = strings.TrimSpace(model)
	if strings.EqualFold(kind, "session") {
		return "Session"
	}
	if model == "" || strings.EqualFold(model, "all models") {
		return "Weekly"
	}
	return model
}

// "used" → remaining = 100-X; "left" → remaining = X.
func findPct(lines []string) int {
	for _, l := range lines {
		m := rePct.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if strings.ToLower(m[2]) == "used" {
			r := 100 - v
			if r < 0 {
				r = 0
			}
			return r
		}
		return v
	}
	return -1
}

func claudeSectionReset(lines []string) time.Time {
	for _, line := range lines {
		resetTime := parseClaudeReset(line)
		isKnown := !resetTime.IsZero()
		if isKnown {
			return resetTime
		}
	}
	return time.Time{}
}

var reClaudeResetRelative = regexp.MustCompile(`(?i)resets?\s+in\s+((?:\d+\s*(?:d|h|m(?:in)?)\s*)+)`)
var reClaudeResetAbsolute = regexp.MustCompile(`(?i)resets?\s+(?:([A-Za-z]{3})\s+(\d{1,2})(?:,\s*(\d{4}))?\s*)?(?:at\s+)?(\d{1,2}(?::\d{2})?\s*(?:am|pm))(?:\s*\(([^)]+)\))?`)

// parseClaudeReset turns a Claude usage line into a future time.Time (zero if
// unparseable). The reset expression is extracted from the line, so trailing
// text painted on the same terminal row is ignored.
func parseClaudeReset(text string) time.Time {
	if text == "" {
		return time.Time{}
	}

	relativeMatch := reClaudeResetRelative.FindStringSubmatch(text)
	if relativeMatch != nil {
		duration := parseRelDuration(relativeMatch[1])
		if duration > 0 {
			return time.Now().Add(duration)
		}
	}

	absoluteMatch := reClaudeResetAbsolute.FindStringSubmatch(text)
	if absoluteMatch == nil {
		return time.Time{}
	}
	resetTime := claudeAbsoluteReset(absoluteMatch)
	return resetTime
}

// Match groups: month, day, year, clock, timezone.
func claudeAbsoluteReset(match []string) time.Time {
	month := match[1]
	day := match[2]
	year := match[3]
	clock := strings.ReplaceAll(match[4], " ", "")
	clock = strings.ToLower(clock)
	zoneName := strings.TrimSpace(match[5])

	location := time.Local
	if zoneName != "" {
		zone, err := time.LoadLocation(zoneName)
		if err == nil {
			location = zone
		}
	}

	body := clock
	formats := []string{"3:04pm", "3pm"}
	if month != "" && year != "" {
		body = month + " " + day + ", " + year + ", " + clock
		formats = []string{"Jan 2, 2006, 3:04pm", "Jan 2, 2006, 3pm"}
	} else if month != "" {
		body = month + " " + day + ", " + clock
		formats = []string{"Jan 2, 3:04pm", "Jan 2, 3pm"}
	}

	now := time.Now().In(location)
	for _, format := range formats {
		parsed, err := time.ParseInLocation(format, body, location)
		if err != nil {
			continue
		}
		resetTime := resolveFuture(parsed, format, now, location)
		return resetTime
	}
	return time.Time{}
}

func parseRelDuration(s string) time.Duration {
	hasDigit := false
	for _, c := range s {
		if c >= '0' && c <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return 0
	}

	var total time.Duration
	matched := false

	if m := regexp.MustCompile(`(\d+)\s*d`).FindStringSubmatch(s); m != nil {
		v, _ := strconv.Atoi(m[1])
		total += time.Duration(v) * 24 * time.Hour
		matched = true
	}
	if m := regexp.MustCompile(`(\d+)\s*h`).FindStringSubmatch(s); m != nil {
		v, _ := strconv.Atoi(m[1])
		total += time.Duration(v) * time.Hour
		matched = true
	}
	if m := regexp.MustCompile(`(\d+)\s*m(?:in)?\b`).FindStringSubmatch(s); m != nil {
		v, _ := strconv.Atoi(m[1])
		total += time.Duration(v) * time.Minute
		matched = true
	}

	if !matched {
		return 0
	}
	return total
}

func resolveFuture(t time.Time, format string, now time.Time, loc *time.Location) time.Time {
	hasYear := strings.Contains(format, "2006")
	hasMonth := strings.Contains(format, "Jan")
	hasTime := strings.Contains(format, "3") || strings.Contains(format, "15")

	if hasYear {
		return t
	}

	if hasMonth {
		candidate := time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		if candidate.After(now) {
			return candidate
		}
		return time.Date(now.Year()+1, t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	}

	if hasTime {
		candidate := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		if candidate.After(now) {
			return candidate
		}
		return candidate.Add(24 * time.Hour)
	}

	return t
}

// Endpoint + headers mirror Claude Code's own fetchUtilization
// (GET /api/oauth/usage with the keychain OAuth access token).
const (
	claudeUsageURL       = "https://api.anthropic.com/api/oauth/usage"
	claudeOAuthBeta      = "oauth-2025-04-20"
	claudeKeychainItem   = "Claude Code-credentials"
	claudeConfigFilename = ".claude.json"
)

type claudeUsageResponse struct {
	FiveHour *claudeWindow `json:"five_hour"`
	SevenDay *claudeWindow `json:"seven_day"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // ms epoch
	} `json:"claudeAiOauth"`
}

type claudeConfig struct {
	CachedUsageUtilization *claudeCachedUsage `json:"cachedUsageUtilization"`
}

type claudeCachedUsage struct {
	FetchedAtMS int64               `json:"fetchedAtMs"`
	Utilization claudeUsageResponse `json:"utilization"`
}

func fetchClaudeCachedUsage() ClaudeStats {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		stats := ClaudeStats{Error: "home directory unavailable"}
		return stats
	}

	configPath := filepath.Join(homeDirectory, claudeConfigFilename)
	body, err := os.ReadFile(configPath)
	if err != nil {
		stats := ClaudeStats{Error: "usage cache unavailable"}
		return stats
	}

	now := time.Now()
	stats := parseClaudeCachedUsage(body, now)
	return stats
}

func parseClaudeCachedUsage(body []byte, now time.Time) ClaudeStats {
	var config claudeConfig
	err := json.Unmarshal(body, &config)
	if err != nil {
		stats := ClaudeStats{Error: "malformed usage cache"}
		return stats
	}

	cachedUsage := config.CachedUsageUtilization
	if cachedUsage == nil {
		stats := ClaudeStats{Error: "usage cache is empty"}
		return stats
	}
	if cachedUsage.FetchedAtMS <= 0 {
		stats := ClaudeStats{Error: "usage cache has no timestamp"}
		return stats
	}

	fetchedAt := time.UnixMilli(cachedUsage.FetchedAtMS)
	if fetchedAt.After(now) {
		stats := ClaudeStats{Error: "usage cache timestamp is in the future"}
		return stats
	}

	stats := claudeStatsFromUsage(cachedUsage.Utilization)
	stats.Session = validCachedClaudeQuota(stats.Session, now)
	stats.Weekly = validCachedClaudeQuota(stats.Weekly, now)
	stats.Available = claudeStatsHasQuota(stats)
	if !stats.Available {
		stats.Error = "usage cache windows have expired"
	}
	return stats
}

func validCachedClaudeQuota(quota QuotaInfo, now time.Time) QuotaInfo {
	if quota.ResetsAt.IsZero() {
		quota.Percent = -1
		return quota
	}
	if quota.ResetsAt.After(now) {
		return quota
	}

	quota.Percent = -1
	quota.ResetsAt = time.Time{}
	return quota
}

func fetchClaudeAPI() ClaudeStats {
	token, err := claudeAccessToken()
	if err != nil {
		return ClaudeStats{Error: "claude: " + err.Error()}
	}

	body, err := claudeUsageRequest(token)
	if err != nil {
		return ClaudeStats{Error: "claude: " + err.Error()}
	}

	return parseClaudeUsage(body)
}

// claudeAccessToken reads the OAuth token Claude Code stores in the macOS keychain.
func claudeAccessToken() (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", claudeKeychainItem, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no keychain credentials (run claude /login)")
	}

	var creds claudeCredentials
	if err := json.Unmarshal(out, &creds); err != nil {
		return "", fmt.Errorf("unreadable keychain credentials")
	}

	oauth := creds.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return "", fmt.Errorf("no OAuth token (run claude /login)")
	}
	if oauth.ExpiresAt > 0 && time.Now().After(time.UnixMilli(oauth.ExpiresAt)) {
		return "", fmt.Errorf("OAuth token expired (start claude to refresh)")
	}
	return oauth.AccessToken, nil
}

func claudeUsageRequest(token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", claudeOAuthBeta)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage API returned %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func parseClaudeUsage(body []byte) ClaudeStats {
	var usage claudeUsageResponse
	err := json.Unmarshal(body, &usage)
	if err != nil {
		return ClaudeStats{Error: "claude: malformed usage response"}
	}

	stats := claudeStatsFromUsage(usage)
	return stats
}

func claudeStatsFromUsage(usage claudeUsageResponse) ClaudeStats {
	session := claudeQuota("Session", usage.FiveHour)
	weekly := claudeQuota("Weekly", usage.SevenDay)
	stats := ClaudeStats{
		Available: true,
		Session:   session,
		Weekly:    weekly,
	}
	return stats
}

// claudeQuota converts the API's utilization (percent used) into remaining percent.
func claudeQuota(label string, w *claudeWindow) QuotaInfo {
	if w == nil {
		return QuotaInfo{Label: label, Percent: -1}
	}

	remaining := 100 - int(w.Utilization)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}

	q := QuotaInfo{Label: label, Percent: remaining}
	if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
		q.ResetsAt = t.Local()
	}
	return q
}
