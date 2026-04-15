// Package codex implements the driver for the OpenAI Codex CLI TUI.
//
// Codex's TUI is a Rust ratatui app rendered into the terminal. It cycles
// through a small set of states that we recognize by substring-matching
// against the rendered screen:
//
//   - Trust prompt:        "Do you trust the contents of this directory"
//   - Resume picker:       "Resume a previous session"
//   - Working:             "esc to interrupt"
//   - Conversation error:  "Conversation interrupted"
//   - Exit summary:        "Token usage:" + "To continue this session"
//   - Idle (post-render):  contains the "OpenAI Codex" header box
//
// The detection rules are checked in priority order so overlapping
// signals (notably the word "Working" inside the trust prompt body)
// don't cause misclassification.
//
// All state strings were captured from real codex sessions; see the
// fixtures in testdata/ for the source frames.
package codex

import (
	"strings"
	"time"

	"github.com/weill-labs/mimic/internal/driver"
)

// Detection substrings — matched against the full rendered screen.
const (
	// trustPromptMarker uniquely identifies the first-run trust prompt.
	// Codex always asks "Do you trust the contents of this directory?".
	trustPromptMarker = "Do you trust"

	// workingMarker is the most reliable working signal: codex always
	// renders "(Ns • esc to interrupt)" while a turn is in flight.
	// Matching only on "Working" would false-positive on the trust
	// prompt body which mentions "Working with untrusted contents".
	workingMarker = "esc to interrupt"

	// errorMarker appears after Esc cancels an in-flight turn.
	errorMarker = "Conversation interrupted"

	// exitedMarker is codex's universal exit signal. After Ctrl+C twice
	// codex prints "To continue this session, run codex resume <id>" and
	// the input box disappears. This is the only marker that's reliably
	// present whether the session was completed normally or cancelled —
	// "Token usage:" only appears when at least one turn ran to completion.
	exitedMarker = "To continue this session"

	// headerMarker is the codex TUI's header box. It's present in every
	// state once the TUI has finished its initial render, so it doubles
	// as a "TUI has rendered" probe — useful for the idle fallback rule.
	headerMarker = "OpenAI Codex"

	// resumePickerMarker is the title of codex's session picker shown by
	// `codex resume`. It's an idle-like state waiting for keyboard input.
	resumePickerMarker = "Resume a previous session"
)

// Driver implements driver.Driver for the codex CLI.
type Driver struct{}

// New constructs a codex driver. The driver is stateless — all state lives
// on the screen tracker that gets passed into DetectState.
func New() *Driver {
	return &Driver{}
}

// init registers the codex driver so main can resolve it via driver.Lookup
// without importing this package directly (main uses a blank import for the
// side effect). --yolo is a mandatory flag for automated use: it disables
// per-tool approval prompts that would otherwise deadlock an orchestrator.
func init() {
	driver.Register(driver.Registration{
		Name:        "codex",
		Binary:      "codex",
		DefaultArgs: []string{"--yolo"},
		Factory:     func() driver.Driver { return New() },
	})
}

// Name returns the agent identifier.
func (d *Driver) Name() string {
	return "codex"
}

// DetectState inspects the screen and returns the matching state.
//
// Rules are checked in priority order:
//
//  1. trust_prompt — most specific; also a superset of "Working" (the trust
//     warning text mentions "Working with untrusted contents") so it must
//     run before the working check.
//  2. exited       — "To continue this session" wins over error because a
//     session may exit while an error notice is still on screen.
//  3. error        — "Conversation interrupted" notice still showing in an
//     active session (input box present).
//  4. working      — "esc to interrupt" footer of an in-flight turn.
//  5. idle         — resume picker or header box present and none of the above.
//  6. starting     — TUI has not rendered yet (very early in session).
func (d *Driver) DetectState(screen driver.Screen) driver.State {
	rendered := screen.Render()

	switch {
	case strings.Contains(rendered, trustPromptMarker):
		return driver.StateTrustPrompt

	case strings.Contains(rendered, exitedMarker):
		return driver.StateExited

	case strings.Contains(rendered, errorMarker):
		return driver.StateError

	case strings.Contains(rendered, workingMarker):
		return driver.StateWorking

	case strings.Contains(rendered, resumePickerMarker):
		return driver.StateIdle

	case strings.Contains(rendered, headerMarker):
		return driver.StateIdle

	default:
		return driver.StateStarting
	}
}

// SubmitPrompt builds the structured prompt submission for codex.
//
// Codex's input box treats fast bursts of characters differently than
// keystrokes — pasting is rendered into a multi-line textarea while
// keystrokes go into the single-line input that submits on Enter. The
// caller is expected to pace body bytes at the returned KeyDelay, then
// pause for SettleDelay before sending the submit bytes.
//
// Trailing newlines on the input are stripped so we don't accidentally
// send the submit key twice.
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
		KeyDelay:    15 * time.Millisecond,
		SettleDelay: time.Second,
	}
}

// ResumePrompt selects the most recent session from codex's resume picker by
// typing "." into the search box and confirming the first match with Enter.
func (d *Driver) ResumePrompt() driver.Submission {
	return driver.Submission{
		Body:        []byte{'.'},
		Submit:      []byte{'\r'},
		KeyDelay:    15 * time.Millisecond,
		SettleDelay: 100 * time.Millisecond,
	}
}

// CancelWork returns the Esc key sequence. Single Esc cancels an in-flight
// turn; double Esc + Ctrl+C are used by codex's exit flow but those aren't
// the orchestrator's responsibility.
func (d *Driver) CancelWork() []byte {
	return []byte{0x1b}
}

// Compile-time assertion that Driver implements driver.Driver.
var _ driver.Driver = (*Driver)(nil)
