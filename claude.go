package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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

func fetchClaude() ClaudeStats {
	raw, err := runClaudeUsageInPTY(20 * time.Second)
	if err != nil {
		return ClaudeStats{Error: "claude: " + err.Error()}
	}

	rendered := renderVT100(raw, 160, 50)
	return parseClaudeUsage(rendered)
}

func runClaudeUsageInPTY(timeout time.Duration) (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude CLI not found")
	}

	cmd := exec.Command(path, "/usage", "--allowed-tools", "")

	// Strip CLAUDE_CODE_OAUTH_TOKEN (inference-only, blocks quota access)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "TERM=xterm-256color", "COLUMNS=160", "LINES=50")

	// Setsid: new session/group leader. Works cleanly with PTY (Setpgid
	// would conflict with PTY's controlling-terminal setup on macOS).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 160})
	if err != nil {
		return "", err
	}
	defer ptmx.Close()

	// Guarantee reap on every return path.
	defer func() {
		killProcessGroup(cmd)
		_ = cmd.Wait()
	}()

	var buf bytes.Buffer
	doneRead := make(chan struct{})
	go func() {
		io.Copy(&buf, ptmx)
		close(doneRead)
	}()

	settle := time.After(8 * time.Second)
	hardKill := time.After(timeout)

	for {
		select {
		case <-settle:
			ptmx.Write([]byte("\r"))
			time.Sleep(300 * time.Millisecond)
			ptmx.Write([]byte("\r"))
			time.Sleep(500 * time.Millisecond)
			ptmx.Write([]byte{0x03}) // Ctrl-C
			killProcessGroup(cmd)
			<-doneRead
			return buf.String(), nil
		case <-hardKill:
			killProcessGroup(cmd)
			return buf.String(), nil
		case <-doneRead:
			return buf.String(), nil
		}
	}
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

func parseClaudeUsage(text string) ClaudeStats {
	stats := ClaudeStats{
		Available: true,
		Session:   QuotaInfo{Label: "Session", Percent: -1},
		Weekly:    QuotaInfo{Label: "Weekly", Percent: -1},
	}

	lines := strings.Split(text, "\n")
	found := false

	for i, line := range lines {
		lower := strings.ToLower(line)

		if strings.Contains(lower, "current session") {
			if pct := findPctWindow(lines, i, 12); pct >= 0 {
				stats.Session.Percent = pct
				stats.Session.ResetsAt = parseClaudeReset(findResetWindow(lines, i, 14))
				found = true
			}
		}

		if strings.Contains(lower, "current week") {
			if pct := findPctWindow(lines, i, 12); pct >= 0 {
				stats.Weekly.Percent = pct
				stats.Weekly.ResetsAt = parseClaudeReset(findResetWindow(lines, i, 14))
				found = true
			}
		}
	}

	if !found {
		stats.Available = false
		stats.Error = "could not parse /usage output"
	}

	return stats
}

// findPctWindow finds first "X% used" or "X% left" within window lines after idx.
// "used" → remaining = 100-X; "left" → remaining = X.
func findPctWindow(lines []string, idx, window int) int {
	end := idx + window
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[idx:end] {
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

func findResetWindow(lines []string, idx, window int) string {
	end := idx + window
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[idx:end] {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "reset") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

var reTzParen = regexp.MustCompile(`\(([^)]+)\)`)

// parseClaudeReset turns Claude reset text into a future time.Time (zero if unparseable).
// Handles: "Resets in 2h 15m", "Resets 4:59pm (America/New_York)",
// "Resets Jan 1, 2026", "Resets Dec 25 at 4:59am (TZ)".
func parseClaudeReset(text string) time.Time {
	if text == "" {
		return time.Time{}
	}

	loc := time.Local
	if m := reTzParen.FindStringSubmatch(text); m != nil {
		if z, err := time.LoadLocation(strings.TrimSpace(m[1])); err == nil {
			loc = z
		}
	}

	body := text
	if idx := strings.LastIndex(strings.ToLower(body), "resets"); idx >= 0 {
		body = body[idx+6:]
	}
	body = reTzParen.ReplaceAllString(body, "")
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, " at ", ", ")
	body = strings.TrimPrefix(strings.ToLower(body), "in ")

	// Try relative duration first ("2h 15m", "2d 3h", "45m")
	if d := parseRelDuration(body); d > 0 {
		return time.Now().Add(d)
	}

	// Try absolute formats
	formats := []string{
		"Jan 2, 2006, 3:04pm",
		"Jan 2, 2006, 3pm",
		"Jan 2, 2006",
		"Jan 2, 3:04pm",
		"Jan 2, 3pm",
		"3:04pm",
		"3pm",
		"Jan 2",
	}
	now := time.Now().In(loc)
	for _, f := range formats {
		t, err := time.ParseInLocation(f, body, loc)
		if err != nil {
			continue
		}
		return resolveFuture(t, f, now, loc)
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
