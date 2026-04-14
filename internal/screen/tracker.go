// Package screen provides a VT-emulator-backed screen state tracker.
// Agent output is fed through the emulator so the driver can query
// what is currently displayed without external screen scraping.
package screen

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/vt"
)

// Tracker maintains a parsed screen buffer by feeding agent PTY output
// through a VT emulator.
//
// The VT emulator answers terminal queries (DSR, DA, cursor position, etc.)
// by writing into an internal pipe. If nobody drains that pipe, the next
// query handler blocks forever while holding the emulator's write lock,
// deadlocking subsequent Render calls. The tracker spawns a background
// drain goroutine to discard those responses — the outer real terminal is
// what actually answers the agent's queries; the tracker's responses are
// purely an artifact of the emulator's design.
type Tracker struct {
	emu *vt.SafeEmulator
}

// NewTracker creates a screen tracker with the given dimensions.
func NewTracker(cols, rows int) *Tracker {
	t := &Tracker{
		emu: vt.NewSafeEmulator(cols, rows),
	}
	// Drain the emulator's response pipe so DSR/DA/etc. handlers don't
	// block holding the write lock. We discard responses; the outer
	// terminal handles real query responses for the agent.
	go func() {
		_, _ = io.Copy(io.Discard, t.emu)
	}()
	return t
}

// Write feeds raw PTY output into the VT emulator. It implements io.Writer
// so it can be used as a tee destination alongside the terminal passthrough.
func (t *Tracker) Write(p []byte) (int, error) {
	return t.emu.Write(p)
}

// Close stops the emulator response drain goroutine.
func (t *Tracker) Close() error {
	return t.emu.Close()
}

// Resize updates the tracked screen dimensions.
func (t *Tracker) Resize(cols, rows int) {
	t.emu.Resize(cols, rows)
}

// Render returns the full screen content as a string.
func (t *Tracker) Render() string {
	return t.emu.Render()
}

// Line returns the text content of a 0-indexed screen row.
func (t *Tracker) Line(row int) string {
	lines := strings.Split(t.emu.Render(), "\n")
	if row < 0 || row >= len(lines) {
		return ""
	}
	return lines[row]
}

// Width returns the screen width.
func (t *Tracker) Width() int {
	return t.emu.Width()
}

// Height returns the screen height.
func (t *Tracker) Height() int {
	return t.emu.Height()
}

// CursorPosition returns the cursor position.
func (t *Tracker) CursorPosition() (x, y int) {
	pos := t.emu.CursorPosition()
	return pos.X, pos.Y
}

// Contains returns true if the screen contains the given substring.
func (t *Tracker) Contains(substr string) bool {
	return strings.Contains(t.emu.Render(), substr)
}
