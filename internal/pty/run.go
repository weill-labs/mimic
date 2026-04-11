// Package pty spawns a process in an inner PTY and passes through I/O
// to the outer terminal, while teeing output to a screen tracker.
package pty

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// OutputObserver receives a copy of all PTY output bytes.
type OutputObserver interface {
	io.Writer
	Resize(cols, rows int)
}

// RunPassthrough spawns the given command in an inner PTY, sets the outer
// terminal to raw mode, and forwards I/O bidirectionally. Output is also
// tee'd to the observer for screen tracking. Returns the child exit code.
func RunPassthrough(name string, args []string, observer OutputObserver) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return 1, err
	}
	defer ptmx.Close()

	// Put outer terminal in raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return 1, err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Sync inner PTY size to outer terminal size.
	syncSize(ptmx, observer)

	// Handle SIGWINCH to keep sizes in sync.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			syncSize(ptmx, observer)
		}
	}()
	defer signal.Stop(sigCh)

	// Bidirectional I/O forwarding.
	var wg sync.WaitGroup

	// stdin → inner PTY
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(ptmx, os.Stdin)
	}()

	// inner PTY → stdout + observer
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := io.MultiWriter(os.Stdout, observer)
		io.Copy(w, ptmx)
	}()

	// Wait for child to exit.
	err = cmd.Wait()
	wg.Wait()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func syncSize(ptmx *os.File, observer OutputObserver) {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}
	pty.Setsize(ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	observer.Resize(cols, rows)
}
