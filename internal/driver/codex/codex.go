// Package codex implements the driver for the OpenAI Codex CLI TUI.
//
// Codex's TUI is a Rust ratatui app rendered into the terminal. It cycles
// through a small set of states that we recognize by substring-matching
// against the rendered screen:
//
//   - Trust prompt:        "Do you trust the contents of this directory"
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

	"github.com/weill-labs/mule/internal/driver"
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
)

// Driver implements driver.Driver for the codex CLI.
type Driver struct{}

// New constructs a codex driver. The driver is stateless — all state lives
// on the screen tracker that gets passed into DetectState.
func New() *Driver {
	return &Driver{}
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
//  5. idle         — header box present and none of the above.
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

	case strings.Contains(rendered, headerMarker):
		return driver.StateIdle

	default:
		return driver.StateStarting
	}
}

// SubmitPrompt builds the byte sequence for typing and submitting prompt.
//
// Codex's input box treats fast bursts of characters differently than
// keystrokes — pasting is rendered into a multi-line textarea while
// keystrokes go into the single-line input that submits on Enter. The
// caller is expected to pace these bytes at KeyDelay() intervals between
// each character, then pause SubmitSettleDelay() after the body before
// the trailing carriage return is delivered.
//
// Trailing newlines on the input are stripped so we don't accidentally
// send the submit key twice.
func (d *Driver) SubmitPrompt(prompt string) []byte {
	trimmed := strings.TrimRight(prompt, "\r\n")
	if trimmed == "" {
		return nil
	}
	out := make([]byte, 0, len(trimmed)+1)
	out = append(out, trimmed...)
	out = append(out, '\r')
	return out
}

// CancelWork returns the Esc key sequence. Single Esc cancels an in-flight
// turn; double Esc + Ctrl+C are used by codex's exit flow but those aren't
// the orchestrator's responsibility.
func (d *Driver) CancelWork() []byte {
	return []byte{0x1b}
}

// KeyDelay is the recommended pause between consecutive prompt-body bytes.
// 15ms is empirically the minimum that codex's input handler accepts as
// keystrokes; faster bursts get treated as paste.
func (d *Driver) KeyDelay() time.Duration {
	return 15 * time.Millisecond
}

// SubmitSettleDelay is the pause after typing the prompt body and before
// the carriage return. Codex's render loop debounces keystroke renders;
// without this pause the Enter key arrives mid-debounce and is consumed
// as part of the input rather than as a submit.
func (d *Driver) SubmitSettleDelay() time.Duration {
	return time.Second
}

// Compile-time assertion that Driver implements driver.Driver.
var _ driver.Driver = (*Driver)(nil)
