//go:build integration

// Integration test that exercises the codex driver against the real codex
// binary in a real PTY. Run with: go test -tags=integration ./internal/driver/codex/...
//
// Skipped by default because it requires:
//   - The codex CLI installed and authenticated (`codex --yolo` works)
//   - Network access to the OpenAI API
//   - Time (~30s end-to-end for one prompt round-trip)
//
// The test does NOT clean up codex's session history; each run leaves a
// new session in ~/.codex/sessions/.
package codex_test

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/mule/internal/driver"
	"github.com/weill-labs/mule/internal/driver/codex"
	"github.com/weill-labs/mule/internal/screen"
)

// waitForState polls DetectState every 100ms until the target state is
// observed or the deadline expires. Returns the last observed state on
// failure so the test message is informative.
func waitForState(t *testing.T, d *codex.Driver, tracker *screen.Tracker, want driver.State, timeout time.Duration) driver.State {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last driver.State
	for time.Now().Before(deadline) {
		last = d.DetectState(tracker)
		if last == want {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}

func TestIntegration_CodexSubmitAndComplete(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("codex not installed: %v", err)
	}

	const cols, rows = 100, 30
	cmd := exec.Command("codex", "--yolo")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		t.Fatalf("start codex: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
	})

	tracker := screen.NewTracker(cols, rows)

	// Tee the agent's PTY into the screen tracker. The goroutine exits
	// when the agent closes the PTY (typically because we killed it).
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = tracker.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	d := codex.New()

	// 1. Wait for codex to reach idle. On a first run codex may show the
	//    trust prompt; we accept idle OR trust_prompt as "ready to drive".
	state := waitForReadyState(t, d, tracker, 30*time.Second)
	if state == driver.StateTrustPrompt {
		// Send Enter to accept the trust default ("Yes, continue"), then
		// wait again for idle.
		if _, err := ptmx.Write([]byte{'\r'}); err != nil {
			t.Fatalf("dismiss trust prompt: %v", err)
		}
		state = waitForState(t, d, tracker, driver.StateIdle, 15*time.Second)
	}
	if state != driver.StateIdle {
		t.Fatalf("codex did not reach idle: last state=%q\n--- screen ---\n%s", state, tracker.Render())
	}

	// 2. Type the prompt char-by-char at the driver's recommended cadence.
	prompt := d.SubmitPrompt("say hi in one word")
	if len(prompt) == 0 {
		t.Fatal("SubmitPrompt returned empty")
	}
	// SubmitPrompt returns body+\r. Send the body bytes one at a time, then
	// the trailing carriage return after a settle delay so codex's render
	// loop processes the body before the submit key arrives.
	body, submit := prompt[:len(prompt)-1], prompt[len(prompt)-1]
	for _, b := range body {
		if _, err := ptmx.Write([]byte{b}); err != nil {
			t.Fatalf("write key %q: %v", b, err)
		}
		time.Sleep(d.KeyDelay())
	}
	time.Sleep(d.SubmitSettleDelay())
	if _, err := ptmx.Write([]byte{submit}); err != nil {
		t.Fatalf("write submit key: %v", err)
	}

	// 3. Verify codex transitions through working → idle.
	if got := waitForState(t, d, tracker, driver.StateWorking, 5*time.Second); got != driver.StateWorking {
		t.Fatalf("codex did not enter working state: last=%q\n--- screen ---\n%s", got, tracker.Render())
	}
	if got := waitForState(t, d, tracker, driver.StateIdle, 60*time.Second); got != driver.StateIdle {
		t.Fatalf("codex did not return to idle: last=%q\n--- screen ---\n%s", got, tracker.Render())
	}
}

// waitForReadyState waits for codex to reach EITHER idle or trust_prompt —
// both signal the driver can begin issuing input.
func waitForReadyState(t *testing.T, d *codex.Driver, tracker *screen.Tracker, timeout time.Duration) driver.State {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last driver.State
	for time.Now().Before(deadline) {
		last = d.DetectState(tracker)
		if last == driver.StateIdle || last == driver.StateTrustPrompt {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}

func TestIntegration_CodexCancelWork(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("codex not installed: %v", err)
	}

	const cols, rows = 100, 30
	cmd := exec.Command("codex", "--yolo")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		t.Fatalf("start codex: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
	})

	tracker := screen.NewTracker(cols, rows)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = tracker.Write(buf[:n])
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
					return
				}
				return
			}
		}
	}()

	d := codex.New()

	state := waitForReadyState(t, d, tracker, 30*time.Second)
	if state == driver.StateTrustPrompt {
		_, _ = ptmx.Write([]byte{'\r'})
		state = waitForState(t, d, tracker, driver.StateIdle, 15*time.Second)
	}
	if state != driver.StateIdle {
		t.Fatalf("codex did not reach idle: %q", state)
	}

	// Submit a long prompt so we have time to cancel.
	body := []byte("write a very long detailed essay about the history of typewriters")
	for _, b := range body {
		_, _ = ptmx.Write([]byte{b})
		time.Sleep(d.KeyDelay())
	}
	time.Sleep(d.SubmitSettleDelay())
	_, _ = ptmx.Write([]byte{'\r'})

	if got := waitForState(t, d, tracker, driver.StateWorking, 5*time.Second); got != driver.StateWorking {
		t.Fatalf("codex did not enter working state: last=%q", got)
	}

	// Cancel mid-work.
	_, _ = ptmx.Write(d.CancelWork())

	if got := waitForState(t, d, tracker, driver.StateError, 5*time.Second); got != driver.StateError {
		t.Fatalf("codex did not enter error state after cancel: last=%q\n--- screen ---\n%s", got, tracker.Render())
	}
}
