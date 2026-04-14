// Package pty spawns a process in an inner PTY and passes through I/O
// to the outer terminal, while teeing output to a screen tracker.
package pty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

// OutputObserver receives a copy of all PTY output bytes.
type OutputObserver interface {
	io.Writer
	Resize(cols, rows int)
}

// Session is a running passthrough PTY session.
type Session struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	oldState *term.State
	stdin    cancelreader.CancelReader
	sigCh    chan os.Signal
	sigDone  chan struct{}

	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// StartPassthrough spawns the given command in an inner PTY, sets the outer
// terminal to raw mode, and forwards I/O bidirectionally. Output is also
// tee'd to the observer for screen tracking.
func StartPassthrough(name string, args []string, observer OutputObserver) (*Session, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	// Put outer terminal in raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		_ = ptmx.Close()
		return nil, err
	}

	stdin, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
		_ = ptmx.Close()
		return nil, err
	}

	session := &Session{
		cmd:      cmd,
		ptmx:     ptmx,
		oldState: oldState,
		stdin:    stdin,
		sigCh:    make(chan os.Signal, 1),
		sigDone:  make(chan struct{}),
	}

	// Sync inner PTY size to outer terminal size.
	syncSize(ptmx, observer)

	// Handle SIGWINCH to keep sizes in sync.
	signal.Notify(session.sigCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-session.sigDone:
				return
			case <-session.sigCh:
				syncSize(ptmx, observer)
			}
		}
	}()

	// Bidirectional I/O forwarding.
	session.wg.Add(1)
	go func() {
		defer session.wg.Done()
		_, _ = io.Copy(ptmx, stdin)
	}()

	session.wg.Add(1)
	go func() {
		defer session.wg.Done()
		w := io.MultiWriter(os.Stdout, observer)
		_, _ = io.Copy(w, ptmx)
	}()

	return session, nil
}

// PTMX returns the inner PTY handle so callers can write control bytes into it.
func (s *Session) PTMX() *os.File {
	return s.ptmx
}

// Wait waits for the child to exit and returns its exit code.
func (s *Session) Wait() (int, error) {
	err := s.cmd.Wait()
	_ = s.Close()
	s.wg.Wait()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// Close tears down the PTY session and restores the outer terminal.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		signal.Stop(s.sigCh)
		close(s.sigDone)

		if s.stdin != nil {
			s.stdin.Cancel()
			if err := s.stdin.Close(); err != nil && s.closeErr == nil {
				s.closeErr = err
			}
		}
		if s.ptmx != nil {
			if err := s.ptmx.Close(); err != nil && s.closeErr == nil && !errors.Is(err, os.ErrClosed) {
				s.closeErr = err
			}
		}
		if s.oldState != nil {
			if err := term.Restore(int(os.Stdin.Fd()), s.oldState); err != nil && s.closeErr == nil {
				s.closeErr = err
			}
		}
	})
	return s.closeErr
}

// RunPassthrough is the compatibility helper that starts a session and waits
// for it to complete.
func RunPassthrough(name string, args []string, observer OutputObserver) (int, error) {
	session, err := StartPassthrough(name, args, observer)
	if err != nil {
		return 1, err
	}
	return session.Wait()
}

func syncSize(ptmx *os.File, observer OutputObserver) {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	observer.Resize(cols, rows)
}
