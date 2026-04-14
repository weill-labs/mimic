// codex-probe spawns a target agent (codex) inside an inner PTY and dumps
// rendered screen frames at intervals so we can study the TUI states for
// driver development. Output goes to /tmp/mimic-probe/.
//
// Run as: go run ./cmd/codex-probe codex --yolo
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/mimic/internal/screen"
)

func log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	f, err := os.OpenFile("/tmp/mimic-probe/probe.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		fmt.Fprintln(f, time.Now().Format("15:04:05.000"), msg)
		f.Close()
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: codex-probe <agent-binary> [args...]\n")
		os.Exit(2)
	}
	binary := os.Args[1]
	args := os.Args[2:]

	outDir := "/tmp/mimic-probe/frames"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail(err)
	}
	os.Remove("/tmp/mimic-probe/raw.log")
	os.Remove("/tmp/mimic-probe/probe.log")
	log("probe starting: %s %v", binary, args)

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	const cols, rows = 100, 30
	log("calling pty.StartWithSize")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		fail(err)
	}
	log("pty.StartWithSize returned, child pid=%d", cmd.Process.Pid)
	defer ptmx.Close()
	defer func() { _ = cmd.Process.Kill() }()

	tracker := screen.NewTracker(cols, rows)

	doneRead := make(chan struct{})
	go func() {
		defer close(doneRead)
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				tracker.Write(buf[:n])
				appendFile("/tmp/mimic-probe/raw.log", buf[:n])
				log("read %d bytes", n)
			}
			if err != nil {
				if err != io.EOF {
					log("read error: %v", err)
				}
				log("read goroutine exiting")
				return
			}
		}
	}()

	captureFrame := func(label string) {
		path := filepath.Join(outDir, label+".txt")
		_ = os.WriteFile(path, []byte(tracker.Render()), 0o644)
		log("captured %s", label)
	}

	// Startup snapshots — codex shows the trust prompt within ~1s on first run.
	time.Sleep(1500 * time.Millisecond)
	captureFrame("01-startup-t1500ms")
	time.Sleep(1500 * time.Millisecond)
	captureFrame("02-startup-t3000ms")

	// Dismiss the trust prompt with Enter (default selection is "Yes, continue").
	log("sending Enter to dismiss trust prompt")
	_, _ = ptmx.Write([]byte{'\r'})
	time.Sleep(500 * time.Millisecond)
	captureFrame("03-after-trust-enter-t500ms")
	time.Sleep(1500 * time.Millisecond)
	captureFrame("04-idle-t2000ms")

	// Type a longer prompt char-by-char so we can capture mid-working frames.
	prompt := "write a 6 line poem about ferrets, slowly thinking step by step"
	log("typing prompt char-by-char (%d chars)...", len(prompt))
	for _, c := range prompt {
		_, _ = ptmx.Write([]byte{byte(c)})
		time.Sleep(15 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	captureFrame("05-typed-prompt")

	time.Sleep(1 * time.Second)
	captureFrame("05b-pre-submit")
	log("submitting prompt with Enter")
	_, _ = ptmx.Write([]byte{'\r'})
	time.Sleep(300 * time.Millisecond)
	captureFrame("06-working-t300ms")
	time.Sleep(700 * time.Millisecond)
	captureFrame("07-working-t1000ms")
	time.Sleep(1 * time.Second)
	captureFrame("08-working-t2000ms")
	time.Sleep(1 * time.Second)
	captureFrame("09-working-t3000ms")

	// Cancel mid-work with single Esc.
	log("sending Esc to interrupt")
	_, _ = ptmx.Write([]byte{0x1b})
	time.Sleep(300 * time.Millisecond)
	captureFrame("10-cancel-t300ms")
	time.Sleep(700 * time.Millisecond)
	captureFrame("11-cancel-t1000ms")
	time.Sleep(1 * time.Second)
	captureFrame("12-cancel-t2000ms")

	_, _ = ptmx.Write([]byte{0x03})
	time.Sleep(200 * time.Millisecond)
	_, _ = ptmx.Write([]byte{0x03})
	time.Sleep(500 * time.Millisecond)
	captureFrame("after-ctrlc")

	log("probe finished, killing child")
	_ = cmd.Process.Kill()
	<-doneRead
	log("probe exit")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func appendFile(path string, data []byte) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
}
