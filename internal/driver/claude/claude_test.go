package claude_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/mimic/internal/driver"
	"github.com/weill-labs/mimic/internal/driver/claude"
)

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
	t.Parallel()

	d := claude.New()

	cases := []struct {
		name    string
		fixture string
		want    driver.State
	}{
		{"blank pre-render screen", "starting_blank.txt", driver.StateStarting},
		{"tool approval prompt", "approval_prompt_create.txt", driver.StateTrustPrompt},
		{"idle typed prompt", "idle_typed_prompt.txt", driver.StateIdle},
		{"working while contemplating", "working_contemplating.txt", driver.StateWorking},
		{"working while churning", "working_churning.txt", driver.StateWorking},
		{"working while whirring", "working_whirring.txt", driver.StateWorking},
		{"error after escape cancel", "error_after_cancel.txt", driver.StateError},
		{"exited after double ctrl+c", "exited_after_ctrlc.txt", driver.StateExited},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			screen := loadFixture(t, tc.fixture)
			got := d.DetectState(screen)
			if got != tc.want {
				t.Errorf("DetectState(%s) = %q, want %q\n--- screen ---\n%s",
					tc.fixture, got, tc.want, screen.Render())
			}
		})
	}
}

func TestDetectStatePriority_ExitedWinsOverError(t *testing.T) {
	t.Parallel()

	d := claude.New()
	screen := loadFixture(t, "exited_after_ctrlc.txt")
	if got := d.DetectState(screen); got != driver.StateExited {
		t.Fatalf("exited fixture detected as %q, want %q", got, driver.StateExited)
	}
}

func TestDetectState_DetectsANSIStyledWorkingStatusLine(t *testing.T) {
	t.Parallel()

	d := claude.New()
	screen := newStringScreen(
		" Claude Code\n" +
			" \x1b[38;2;215;119;87m*\x1b[39m \x1b[38;2;215;119;87mCanoodling… \x1b[38;2;164;164;164m(thinking)\n",
	)

	if got := d.DetectState(screen); got != driver.StateWorking {
		t.Fatalf("DetectState(ansi working status) = %q, want %q\n--- screen ---\n%s", got, driver.StateWorking, screen.Render())
	}
}

func TestDetectState_DetectsSpinnerOnlyStatusLine(t *testing.T) {
	t.Parallel()

	d := claude.New()
	screen := newStringScreen(" Claude Code\n ✶\n")

	if got := d.DetectState(screen); got != driver.StateWorking {
		t.Fatalf("DetectState(spinner only) = %q, want %q\n--- screen ---\n%s", got, driver.StateWorking, screen.Render())
	}
}

func TestDetectState_DetectsStreamingTranscriptWithoutHeader(t *testing.T) {
	t.Parallel()

	d := claude.New()
	screen := newStringScreen(
		"The typewriter's story begins long before the machines we recognize today.\n" +
			"The earliest known patent for a writing machine was granted in 1714.\n" +
			"Inventors across Europe and America kept iterating on the idea.\n" +
			"\n" +
			"────────────────────────────────────────────────────────────────────────────────\n" +
			"❯ \n" +
			"────────────────────────────────────────────────────────────────────────────────\n" +
			"[Apr 15 08:27:40]\n" +
			"Now using extra usage\n" +
			"You're now using extra usage · Your session limit resets 9am (UTC)\n" +
			"● high · /effort\n",
	)

	if got := d.DetectState(screen); got != driver.StateWorking {
		t.Fatalf("DetectState(streaming transcript) = %q, want %q\n--- screen ---\n%s", got, driver.StateWorking, screen.Render())
	}
}

func TestName(t *testing.T) {
	t.Parallel()

	d := claude.New()
	if d.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", d.Name(), "claude")
	}
}

func TestSubmitPrompt(t *testing.T) {
	t.Parallel()

	d := claude.New()
	got := d.SubmitPrompt("hi")
	if !bytes.Equal(got.Body, []byte("hi")) {
		t.Errorf("SubmitPrompt(%q).Body = %q, want %q", "hi", got.Body, []byte("hi"))
	}
	if !bytes.Equal(got.Submit, []byte{'\r'}) {
		t.Errorf("SubmitPrompt(%q).Submit = %q, want %q", "hi", got.Submit, []byte{'\r'})
	}
	if got.KeyDelay != 40*time.Millisecond {
		t.Errorf("SubmitPrompt(%q).KeyDelay = %v, want %v", "hi", got.KeyDelay, 40*time.Millisecond)
	}
	if got.SettleDelay != 200*time.Millisecond {
		t.Errorf("SubmitPrompt(%q).SettleDelay = %v, want %v", "hi", got.SettleDelay, 200*time.Millisecond)
	}
}

func TestSubmitPrompt_StripsTrailingNewline(t *testing.T) {
	t.Parallel()

	d := claude.New()
	got := d.SubmitPrompt("hi\n")
	if !bytes.Equal(got.Body, []byte("hi")) {
		t.Errorf("SubmitPrompt(%q).Body = %q, want %q", "hi\n", got.Body, []byte("hi"))
	}
	if !bytes.Equal(got.Submit, []byte{'\r'}) {
		t.Errorf("SubmitPrompt(%q).Submit = %q, want %q", "hi\n", got.Submit, []byte{'\r'})
	}
}

func TestSubmitPrompt_EmptyIsEmpty(t *testing.T) {
	t.Parallel()

	d := claude.New()
	got := d.SubmitPrompt("")
	if len(got.Body) != 0 || len(got.Submit) != 0 || got.KeyDelay != 0 || got.SettleDelay != 0 {
		t.Errorf("SubmitPrompt(\"\") = %+v, want zero-value submission", got)
	}
}

func TestCancelWork(t *testing.T) {
	t.Parallel()

	d := claude.New()
	got := d.CancelWork()
	want := []byte{0x1b}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("CancelWork() = %v, want %v", got, want)
	}
}

func TestRegistration(t *testing.T) {
	t.Parallel()

	resolved, err := driver.Lookup("claude")
	if err != nil {
		t.Fatalf("Lookup(claude): %v", err)
	}
	if resolved.Binary != "claude" {
		t.Fatalf("Lookup(claude).Binary = %q, want %q", resolved.Binary, "claude")
	}
	want := []string{"--permission-mode", "default"}
	if len(resolved.DefaultArgs) != len(want) {
		t.Fatalf("Lookup(claude).DefaultArgs = %v, want %v", resolved.DefaultArgs, want)
	}
	for i := range want {
		if resolved.DefaultArgs[i] != want[i] {
			t.Fatalf("Lookup(claude).DefaultArgs[%d] = %q, want %q", i, resolved.DefaultArgs[i], want[i])
		}
	}
	if resolved.Driver == nil || resolved.Driver.Name() != "claude" {
		t.Fatalf("Lookup(claude).Driver = %+v, want driver named claude", resolved.Driver)
	}
}
