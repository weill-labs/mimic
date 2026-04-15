package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/weill-labs/mimic/internal/api"
	"github.com/weill-labs/mimic/internal/driver"
	"github.com/weill-labs/mimic/internal/pty"
	"github.com/weill-labs/mimic/internal/screen"

	// Side-effect imports: each driver package registers itself in its
	// init() so driver.Lookup can find it. Add new driver packages here.
	_ "github.com/weill-labs/mimic/internal/driver/claude"
	_ "github.com/weill-labs/mimic/internal/driver/codex"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		printUsage()
		return 2
	}

	agent := os.Args[1]
	socketPath, resume, extraArgs, err := parseCLIArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mimic: %v\n", err)
		printUsage()
		return 2
	}

	// Resolve the agent via the driver registry. This gives us both the
	// driver implementation (for state detection / submit / cancel) and the
	// binary+args needed to actually spawn the agent process.
	resolved, err := driver.Lookup(agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mimic: %v\n", err)
		return 1
	}

	// Build the final arg vector: driver-mandated defaults first, then
	// whatever the user passed. Defaults like codex's --yolo can't be
	// overridden because they'd break the orchestrator (approval prompts
	// would deadlock the driver).
	binaryArgs := append([]string(nil), resolved.DefaultArgs...)
	if resume {
		binaryArgs = append(binaryArgs, "resume")
	}
	binaryArgs = append(binaryArgs, extraArgs...)

	// Create screen tracker (VT emulator for internal screen state).
	tracker := screen.NewTracker(80, 24)
	defer tracker.Close()

	session, err := pty.StartPassthrough(resolved.Binary, binaryArgs, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mimic: %v\n", err)
		return 1
	}
	defer session.Close()

	if resume {
		if err := runResumeFlow(resolved.Driver, tracker, session.PTMX()); err != nil {
			fmt.Fprintf(os.Stderr, "mimic: %v\n", err)
			return 1
		}
	}

	var dispatcher *api.Dispatcher
	var server *api.Server

	if socketPath != "" {
		dispatcher = api.NewDispatcher(resolved.Driver, tracker, session.PTMX())
		defer dispatcher.Close()

		server, err = api.NewServer(socketPath, dispatcher)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mimic: %v\n", err)
			return 1
		}
		defer server.Close()
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	go func() {
		<-shutdownCtx.Done()
		if server != nil {
			_ = server.Close()
		}
		if dispatcher != nil {
			dispatcher.Close()
		}
		_ = session.Close()
	}()

	exitCode, err := session.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mimic: %v\n", err)
		return 1
	}
	return exitCode
}

// registeredAgentsList returns a comma-separated list of registered agents
// for usage text. Called only on the error path so the formatting cost is
// irrelevant.
func registeredAgentsList() string {
	names := driver.Registered()
	if len(names) == 0 {
		return "(none — build mimic with a driver side-effect import)"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "usage: mimic <agent> [--socket PATH] [--resume] [-- agent-args...]\n")
	fmt.Fprintf(os.Stderr, "available agents: %s\n", registeredAgentsList())
}

func parseCLIArgs(args []string) (string, bool, []string, error) {
	var socketPath string
	var resume bool
	var extraArgs []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			extraArgs = append(extraArgs, args[i+1:]...)
			return socketPath, resume, extraArgs, nil
		case "--resume":
			resume = true
		case "--socket":
			if i+1 >= len(args) {
				return "", false, nil, fmt.Errorf("--socket requires a path")
			}
			socketPath = args[i+1]
			i++
		default:
			extraArgs = append(extraArgs, args[i])
		}
	}

	return socketPath, resume, extraArgs, nil
}

const (
	resumePollInterval  = 100 * time.Millisecond
	resumeRenderTimeout = 10 * time.Second
	trustDismissPause   = 500 * time.Millisecond
)

func runResumeFlow(d driver.Driver, screen driver.Screen, ptmx io.Writer) error {
	submission := d.ResumePrompt()
	if len(submission.Body) == 0 && len(submission.Submit) == 0 {
		return fmt.Errorf("agent %q does not support --resume", d.Name())
	}

	deadline := time.Now().Add(resumeRenderTimeout)
	for time.Now().Before(deadline) {
		state := d.DetectState(screen)
		switch state {
		case driver.StateIdle:
			return writeSubmission(ptmx, submission)
		case driver.StateTrustPrompt:
			if err := writeAll(ptmx, []byte{'\r'}); err != nil {
				return err
			}
			time.Sleep(trustDismissPause)
		case driver.StateStarting, driver.StateUnknown:
			time.Sleep(resumePollInterval)
		default:
			return fmt.Errorf("resume flow blocked in state %q", state)
		}
	}

	return errors.New("resume picker did not render before timeout")
}

func writeSubmission(w io.Writer, submission driver.Submission) error {
	for i, b := range submission.Body {
		if err := writeAll(w, []byte{b}); err != nil {
			return err
		}
		if i == len(submission.Body)-1 || submission.KeyDelay <= 0 {
			continue
		}
		time.Sleep(submission.KeyDelay)
	}

	if submission.SettleDelay > 0 {
		time.Sleep(submission.SettleDelay)
	}
	return writeAll(w, submission.Submit)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
