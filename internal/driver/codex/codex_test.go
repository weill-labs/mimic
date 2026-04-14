package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/mimic/internal/driver"
	"github.com/weill-labs/mimic/internal/driver/codex"
)

// stringScreen is a minimal driver.Screen implementation backed by a fixed
// rendered string. It's how unit tests feed recorded codex frames into the
// driver without spinning up a real VT emulator.
type stringScreen struct {
	text   string
	width  int
	height int
}

func newStringScreen(text string) *stringScreen {
	lines := strings.Split(text, "\n")
	maxWidth := 0
	for _, line := range lines {
		if len(line) > maxWidth {
			maxWidth = len(line)
		}
	}
	return &stringScreen{text: text, width: maxWidth, height: len(lines)}
}

func (s *stringScreen) Render() string              { return s.text }
func (s *stringScreen) Contains(substr string) bool { return strings.Contains(s.text, substr) }
func (s *stringScreen) Width() int                  { return s.width }
func (s *stringScreen) Height() int                 { return s.height }
func (s *stringScreen) Line(row int) string {
	lines := strings.Split(s.text, "\n")
	if row < 0 || row >= len(lines) {
		return ""
	}
	return lines[row]
}

func loadFixture(t *testing.T, name string) *stringScreen {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return newStringScreen(string(data))
}

func TestDetectState(t *testing.T) {
	d := codex.New()

	cases := []struct {
		name    string
		fixture string
		want    driver.State
	}{
		{"blank pre-render screen", "starting_blank.txt", driver.StateStarting},
		{"trust prompt on first run", "trust_prompt.txt", driver.StateTrustPrompt},
		{"idle with typed prompt", "idle_typed_prompt.txt", driver.StateIdle},
		{"working at t+300ms", "working_t300ms.txt", driver.StateWorking},
		{"working at t+1s", "working_t1000ms.txt", driver.StateWorking},
		{"error after escape cancel", "error_after_cancel.txt", driver.StateError},
		{"exited after ctrl+c", "exited_after_ctrlc.txt", driver.StateExited},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen := loadFixture(t, tc.fixture)
			got := d.DetectState(screen)
			if got != tc.want {
				t.Errorf("DetectState(%s) = %q, want %q\n--- screen ---\n%s",
					tc.fixture, got, tc.want, screen.Render())
			}
		})
	}
}

// TestDetectStatePriority verifies that overlapping signals are resolved by
// the documented priority order. The trust prompt text contains the literal
// word "Working" — the driver must NOT misclassify it as working state.
func TestDetectStatePriority(t *testing.T) {
	d := codex.New()
	screen := loadFixture(t, "trust_prompt.txt")
	if got := d.DetectState(screen); got != driver.StateTrustPrompt {
		t.Fatalf("trust prompt fixture detected as %q, want %q", got, driver.StateTrustPrompt)
	}
}

func TestName(t *testing.T) {
	d := codex.New()
	if d.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", d.Name(), "codex")
	}
}

func TestSubmitPrompt(t *testing.T) {
	d := codex.New()
	got := d.SubmitPrompt("hi")
	want := []byte("hi\r")
	if string(got) != string(want) {
		t.Errorf("SubmitPrompt(%q) = %q, want %q", "hi", got, want)
	}
}

func TestSubmitPrompt_StripsTrailingNewline(t *testing.T) {
	// Trailing whitespace/newlines should be stripped before the carriage
	// return is appended — otherwise we send "hi\n\r" which codex's input
	// box treats differently than a clean submit.
	d := codex.New()
	got := d.SubmitPrompt("hi\n")
	want := []byte("hi\r")
	if string(got) != string(want) {
		t.Errorf("SubmitPrompt(%q) = %q, want %q", "hi\n", got, want)
	}
}

func TestSubmitPrompt_EmptyIsEmpty(t *testing.T) {
	d := codex.New()
	if got := d.SubmitPrompt(""); len(got) != 0 {
		t.Errorf("SubmitPrompt(\"\") = %q, want empty", got)
	}
}

func TestCancelWork(t *testing.T) {
	d := codex.New()
	got := d.CancelWork()
	want := []byte{0x1b}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("CancelWork() = %v, want %v", got, want)
	}
}

func TestKeyDelay(t *testing.T) {
	d := codex.New()
	if got := d.KeyDelay(); got <= 0 || got > 100*time.Millisecond {
		t.Errorf("KeyDelay() = %v, want a small positive duration", got)
	}
}

func TestSubmitSettleDelay(t *testing.T) {
	d := codex.New()
	if got := d.SubmitSettleDelay(); got < 100*time.Millisecond {
		t.Errorf("SubmitSettleDelay() = %v, want at least 100ms", got)
	}
}
