// Package driver defines the per-agent state machine interface.
//
// Each agent (codex, claude, ...) has its own driver in a sub-package.
// A driver inspects the screen tracker to detect what state the agent
// TUI is currently in, and produces input byte sequences for submitting
// prompts and cancelling work.
package driver

import "time"

// State enumerates the high-level states an agent TUI can be in.
//
// Drivers map their concrete TUI patterns to this shared vocabulary so
// orchestrators can reason about agents uniformly.
type State string

const (
	// StateUnknown is returned when the screen does not match any known pattern.
	// Typical at very start of a session before the TUI has rendered anything.
	StateUnknown State = "unknown"

	// StateStarting means the agent process is alive but the TUI is not yet
	// fully drawn. Distinct from Unknown only when the driver can recognize
	// an explicit splash/loading screen.
	StateStarting State = "starting"

	// StateTrustPrompt means the agent is waiting for the user to approve
	// running in the current directory (first-run safety prompt).
	StateTrustPrompt State = "trust_prompt"

	// StateIdle means the agent is showing its input prompt and waiting
	// for user input. No work in progress.
	StateIdle State = "idle"

	// StateWorking means the agent is actively processing a prompt
	// (calling the model, running tools, thinking).
	StateWorking State = "working"

	// StateError means the agent is in a recoverable error state — typically
	// after an interrupt — and showing an error/interrupt notice. The agent
	// is still alive and can accept new input.
	StateError State = "error"

	// StateExited means the agent process has printed its goodbye/summary
	// message and is about to exit (or already has).
	StateExited State = "exited"
)

// Screen is the read-only view of the agent's TUI that drivers use for
// state detection. The screen package's *Tracker satisfies this.
type Screen interface {
	// Render returns the full screen as a string with one line per row.
	// Includes ANSI escape sequences as the emulator preserved them.
	Render() string
	// Contains reports whether the rendered screen contains substr.
	Contains(substr string) bool
	// Line returns the text of a 0-indexed row.
	Line(row int) string
	// Width returns the screen width in cells.
	Width() int
	// Height returns the screen height in cells.
	Height() int
}

// Submission describes how to type and submit a prompt.
type Submission struct {
	// Body contains the prompt bytes to type before submission.
	Body []byte

	// Submit contains the final key sequence that submits the prompt.
	Submit []byte

	// KeyDelay is the recommended pause between consecutive body bytes.
	KeyDelay time.Duration

	// SettleDelay is the recommended pause after typing the body and
	// before writing the submit bytes.
	SettleDelay time.Duration
}

// Driver is the interface every agent driver implements.
type Driver interface {
	// Name is a stable identifier for the agent (e.g. "codex", "claude").
	Name() string

	// DetectState inspects the current screen state and returns the
	// best-matching State. Drivers should never block.
	DetectState(screen Screen) State

	// SubmitPrompt returns the structured prompt body, submit bytes, and
	// the recommended pacing between them.
	SubmitPrompt(prompt string) Submission

	// ResumePrompt returns the structured input sequence that selects the
	// resume target after the agent's resume UI has rendered.
	ResumePrompt() Submission

	// CancelWork returns the byte sequence to cancel in-progress work.
	// For most agents this is a single Esc; some may use Ctrl+C.
	CancelWork() []byte
}
