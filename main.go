package main

import (
	"fmt"
	"os"

	"github.com/weill-labs/mule/internal/driver"
	"github.com/weill-labs/mule/internal/pty"
	"github.com/weill-labs/mule/internal/screen"

	// Side-effect imports: each driver package registers itself in its
	// init() so driver.Lookup can find it. Add new driver packages here.
	_ "github.com/weill-labs/mule/internal/driver/codex"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mule <agent> [--socket PATH] [-- agent-args...]\n")
		fmt.Fprintf(os.Stderr, "available agents: %s\n", registeredAgentsList())
		os.Exit(2)
	}

	agent := os.Args[1]
	extraArgs := os.Args[2:]

	// Resolve the agent via the driver registry. This gives us both the
	// driver implementation (for future state detection / submit / cancel)
	// and the binary+args needed to actually spawn the agent process.
	resolved, err := driver.Lookup(agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mule: %v\n", err)
		os.Exit(1)
	}

	// TODO(phase-3): hand `resolved.Driver` to the socket dispatcher once
	// the Unix socket API (weill-labs/mule#2) lands. For now the driver is
	// resolved only to validate the agent name at startup.
	_ = resolved.Driver

	// Build the final arg vector: driver-mandated defaults first, then
	// whatever the user passed. Defaults like codex's --yolo can't be
	// overridden because they'd break the orchestrator (approval prompts
	// would deadlock the driver).
	binaryArgs := append([]string(nil), resolved.DefaultArgs...)
	binaryArgs = append(binaryArgs, extraArgs...)

	// Create screen tracker (VT emulator for internal screen state).
	tracker := screen.NewTracker(80, 24)

	// Spawn agent in inner PTY with passthrough + screen tracking.
	exitCode, err := pty.RunPassthrough(resolved.Binary, binaryArgs, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mule: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

// registeredAgentsList returns a comma-separated list of registered agents
// for usage text. Called only on the error path so the formatting cost is
// irrelevant.
func registeredAgentsList() string {
	names := driver.Registered()
	if len(names) == 0 {
		return "(none — build mule with a driver side-effect import)"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
