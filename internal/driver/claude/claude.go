// Package claude implements the driver for the Claude Code TUI.
//
// Claude Code's terminal UI shares the same high-level lifecycle as codex,
// but the concrete screens differ:
//
//   - Tool approval prompt: "Do you want to …" + "Tab to amend"
//   - Working: observed spinner verbs such as "Contemplating…", "Churning…",
//     "Spinning…", "Whirring…", and "Beboppin'…"
//   - Interrupt notice: "Interrupted · What should Claude do instead?"
//   - Exit summary: "Resume this session with:"
//   - Idle (post-render): contains the "Claude Code" header
//
// The approval prompt is mapped onto the shared StateTrustPrompt bucket so the
// existing dispatcher can auto-press Enter to approve it. Claude's spinner
// verb is intentionally whimsical and not stable across versions; see the TODO
// on working detection below.
package claude

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/weill-labs/mimic/internal/driver"
)

const (
	headerMarker = "Claude Code"

	approvalQuestionMarker = "Do you want to "
	approvalHintMarker     = "Tab to amend"
	activityFooterMarker   = "● high · /effort"

	errorMarker  = "What should Claude do instead?"
	exitedMarker = "Resume this session with:"
)

var workingMarkers = []string{
	"Contemplating…",
	"Churning…",
	"Beboppin'…",
	"Spinning…",
	"Spinning...",
	"Whirring…",
	"Whirring...",
	"Running…",
	"Running...",
}

// Driver implements driver.Driver for the Claude Code CLI.
type Driver struct{}

// New constructs a claude driver.
func New() *Driver {
	return &Driver{}
}

// init registers the claude driver so main can resolve it via driver.Lookup.
//
// We force permission mode to "default" so Claude consistently surfaces its
// per-tool approval dialogs regardless of user-local settings. The dispatcher
// auto-dismisses those dialogs by mapping them to StateTrustPrompt.
func init() {
	driver.Register(driver.Registration{
		Name:        "claude",
		Binary:      "claude",
		DefaultArgs: []string{"--permission-mode", "default"},
		Factory:     func() driver.Driver { return New() },
	})
}

// Name returns the agent identifier.
func (d *Driver) Name() string {
	return "claude"
}

// DetectState inspects the screen and returns the matching state.
//
// Rules are checked in priority order:
//
//  1. exited       — Claude's exit summary still includes the prior transcript,
//     so it can coexist with interrupt/error text.
//  2. trust_prompt — per-tool approval dialog that expects Enter/Esc/Tab.
//  3. error        — interrupted turn waiting for a follow-up prompt.
//  4. working      — observed live spinner/status markers from real sessions.
//  5. idle         — header is visible and none of the above matched.
//  6. starting     — TUI has not rendered yet.
func (d *Driver) DetectState(screen driver.Screen) driver.State {
	rendered := stripANSI(screen.Render())

	switch {
	case strings.Contains(rendered, exitedMarker):
		return driver.StateExited

	case strings.Contains(rendered, approvalQuestionMarker) &&
		strings.Contains(rendered, approvalHintMarker):
		return driver.StateTrustPrompt

	case strings.Contains(rendered, errorMarker):
		return driver.StateError

	case containsAny(rendered, workingMarkers), hasWorkingStatusLine(rendered):
		// TODO(LAB-1188): capture a broader set of Claude spinner verbs.
		// v2.1.109 was observed emitting "Contemplating…", "Churning…",
		// "Spinning…", "Whirring…", and "Beboppin'…", but the vocabulary
		// is not stable across turns.
		return driver.StateWorking

	case hasStreamingTranscript(rendered):
		// Long answers can scroll the header out of view after the spinner
		// status line disappears but before Claude emits its final completion
		// summary. While that layout is visible, the turn is still active.
		return driver.StateWorking

	case strings.Contains(rendered, headerMarker):
		return driver.StateIdle

	default:
		return driver.StateStarting
	}
}

// SubmitPrompt builds the structured prompt submission for Claude Code.
//
// Real PTY probes showed Claude dropping spaces when keys arrived too quickly.
// A slower 40ms cadence kept prompts intact, and a short settle delay gave the
// final characters time to render before Enter submitted the turn.
func (d *Driver) SubmitPrompt(prompt string) driver.Submission {
	trimmed := strings.TrimRight(prompt, "\r\n")
	if trimmed == "" {
		return driver.Submission{}
	}

	body := make([]byte, 0, len(trimmed))
	body = append(body, trimmed...)
	return driver.Submission{
		Body:        body,
		Submit:      []byte{'\r'},
		KeyDelay:    40 * time.Millisecond,
		SettleDelay: 200 * time.Millisecond,
	}
}

// CancelWork returns the Esc key sequence. Real Claude Code sessions entered
// the interrupted follow-up state after a single Esc while a turn was running.
func (d *Driver) CancelWork() []byte {
	return []byte{0x1b}
}

// ResumePrompt returns an empty submission. Claude Code does not currently
// have a session resume picker like codex does.
func (d *Driver) ResumePrompt() driver.Submission {
	return driver.Submission{}
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func hasWorkingStatusLine(rendered string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || len(fields) > 4 {
			continue
		}
		if !isSpinnerToken(fields[0]) {
			continue
		}
		if len(fields) == 1 {
			return true
		}
		if isThinkingNote(fields[1]) {
			return true
		}
		if !isWorkingVerb(fields[1]) {
			continue
		}
		switch len(fields) {
		case 2:
			return true
		case 3:
			if isThinkingNote(fields[2]) {
				return true
			}
		}
	}
	return false
}

func isSpinnerToken(token string) bool {
	switch token {
	case "✢", "·", "*", "✶", "✻", "✽", "●":
		return true
	default:
		return false
	}
}

func isWorkingVerb(token string) bool {
	switch {
	case token == "Thinking":
		return true
	case strings.HasSuffix(token, "…"):
		token = strings.TrimSuffix(token, "…")
	case strings.HasSuffix(token, "..."):
		token = strings.TrimSuffix(token, "...")
	default:
		return false
	}

	if token == "" {
		return false
	}

	first, size := utf8.DecodeRuneInString(token)
	if !unicode.IsUpper(first) {
		return false
	}
	for _, r := range token[size:] {
		if !unicode.IsLetter(r) && r != '\'' {
			return false
		}
	}
	return true
}

func isThinkingNote(token string) bool {
	return strings.HasPrefix(token, "(") && strings.HasSuffix(token, ")")
}

func hasStreamingTranscript(rendered string) bool {
	if strings.Contains(rendered, headerMarker) {
		return false
	}
	if !strings.Contains(rendered, activityFooterMarker) {
		return false
	}
	return countContentLines(rendered) >= 3
}

func countContentLines(rendered string) int {
	count := 0
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "─") || strings.HasPrefix(trimmed, "❯") {
			continue
		}
		if strings.Contains(trimmed, activityFooterMarker) {
			continue
		}
		if strings.HasPrefix(trimmed, "[Apr ") || strings.HasPrefix(trimmed, "Now using extra usage") || strings.HasPrefix(trimmed, "You're now using extra usage") {
			continue
		}
		count++
	}
	return count
}

func stripANSI(s string) string {
	b := strings.Builder{}
	b.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteByte(s[i])
				i++
				continue
			}
			b.WriteRune(r)
			i += size
			continue
		}

		i++
		if i >= len(s) {
			break
		}

		switch s[i] {
		case '[':
			i++
			for i < len(s) {
				ch := s[i]
				i++
				if ch >= 0x40 && ch <= 0x7e {
					break
				}
			}
		case ']':
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			i++
		}
	}

	return b.String()
}

var _ driver.Driver = (*Driver)(nil)
