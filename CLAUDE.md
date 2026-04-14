# CLAUDE.md

## Project tracking

Linear project: https://linear.app/weill-labs/project/mimic-0f918bb42136

## What is mimic?

A PTY driver for AI coding agents. It spawns agent TUIs (codex, claude) inside an inner PTY, passes through all I/O for visual display, and provides a Unix socket API for programmatic control. The VT emulator tracks screen state internally so the driver knows what the agent is doing without external screen scraping.

## Architecture

```
outer terminal ←→ mimic ←→ inner PTY ←→ agent TUI (codex/claude)
                    ↕
              Unix socket ←→ orchestrator (orca)
```

### Key components

- **PTY passthrough** (`internal/pty/`): Allocates inner PTY, spawns agent, forwards I/O bidirectionally.
- **Screen tracker** (`internal/screen/`): Feeds agent output through `weill-labs/x/vt` to maintain parsed screen state.
- **Driver interface** (`internal/driver/`): Per-agent state machines. Each driver defines states, transition patterns, and input sequences.
- **Socket API** (`internal/api/`): Unix socket listener, JSON message protocol.
- **CLI** (`main.go`): Entry point. Parses args, wires components, runs the event loop.

### Drivers

Each agent has a driver in `internal/driver/<agent>/`. A driver implements:

```go
type Driver interface {
    // DetectState reads the current screen and returns the agent's state.
    DetectState(screen Screen) State
    // SubmitPrompt returns the key sequence to type a prompt and submit it.
    SubmitPrompt(prompt string) []KeyEvent
    // CancelWork returns the key sequence to cancel in-progress work.
    CancelWork() []KeyEvent
}
```

## Development

```bash
go build -o mimic .
go test ./...
```

## Testing

Driver tests must exercise real agent binaries in real PTYs. Use recorded VT output for unit tests of state detection. Integration tests spawn the actual agent and verify the full submit/complete cycle.
