package main

import (
	"fmt"
	"os"

	"github.com/weill-labs/mule/internal/pty"
	"github.com/weill-labs/mule/internal/screen"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mule <agent> [--socket PATH] [-- agent-args...]\n")
		os.Exit(2)
	}

	agent := os.Args[1]
	agentArgs := os.Args[2:]

	// Resolve agent binary.
	binary, err := resolveAgent(agent, agentArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mule: %v\n", err)
		os.Exit(1)
	}

	// Create screen tracker (VT emulator for internal screen state).
	tracker := screen.NewTracker(80, 24)

	// Spawn agent in inner PTY with passthrough + screen tracking.
	exitCode, err := pty.RunPassthrough(binary.name, binary.args, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mule: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

type agentBinary struct {
	name string
	args []string
}

func resolveAgent(agent string, extraArgs []string) (agentBinary, error) {
	switch agent {
	case "codex":
		return agentBinary{name: "codex", args: extraArgs}, nil
	case "claude":
		return agentBinary{name: "claude", args: extraArgs}, nil
	default:
		return agentBinary{}, fmt.Errorf("unknown agent %q (supported: codex, claude)", agent)
	}
}
