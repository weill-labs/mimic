package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	socketPath, extraArgs, err := parseCLIArgs(os.Args[2:])
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
	fmt.Fprintf(os.Stderr, "usage: mimic <agent> [--socket PATH] [-- agent-args...]\n")
	fmt.Fprintf(os.Stderr, "available agents: %s\n", registeredAgentsList())
}

func parseCLIArgs(args []string) (string, []string, error) {
	var socketPath string
	var extraArgs []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			extraArgs = append(extraArgs, args[i+1:]...)
			return socketPath, extraArgs, nil
		case "--socket":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--socket requires a path")
			}
			socketPath = args[i+1]
			i++
		default:
			extraArgs = append(extraArgs, args[i])
		}
	}

	return socketPath, extraArgs, nil
}
