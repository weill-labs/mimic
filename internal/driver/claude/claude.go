// Package claude implements the driver for the Claude Code TUI.
//
// Claude Code's terminal UI shares the same high-level lifecycle as codex,
// but the concrete screens differ:
//
//   - Tool approval prompt: "Do you want to …" + "Tab to amend"
//   - Working: observed spinner verbs such as "Contemplating…", "Churning…",
//     and "Beboppin'…"
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

	"github.com/weill-labs/mimic/internal/driver"
)

const (
	headerMarker = "Claude Code"

	approvalQuestionMarker = "Do you want to "
	approvalHintMarker     = "Tab to amend"

	errorMarker  = "What should Claude do instead?"
	exitedMarker = "Resume this session with:"
)

var workingMarkers = []string{
	"Contemplating…",
	"Churning…",
	"Beboppin'…",
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
	rendered := screen.Render()

	switch {
	case strings.Contains(rendered, exitedMarker):
		return driver.StateExited

	case strings.Contains(rendered, approvalQuestionMarker) &&
		strings.Contains(rendered, approvalHintMarker):
		return driver.StateTrustPrompt

	case strings.Contains(rendered, errorMarker):
		return driver.StateError

	case containsAny(rendered, workingMarkers):
		// TODO(LAB-1188): capture a broader set of Claude spinner verbs.
		// v2.1.109 was observed emitting "Contemplating…", "Churning…", and
		// "Beboppin'…", but the vocabulary is not stable across turns.
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

var _ driver.Driver = (*Driver)(nil)
