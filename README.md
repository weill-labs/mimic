# mule

PTY driver for AI coding agents. Spawns agent TUIs with programmatic control via Unix socket, while keeping the full visual TUI visible.

## How it works

```
Terminal (amux pane, tmux pane, bare terminal)
  └── mule codex --socket /tmp/mule-task.sock
        ├── inner PTY ←→ codex (the real agent TUI)
        ├── VT emulator (parses output, tracks screen state)
        ├── state machine (starting → idle → working → complete)
        └── Unix socket (submit/status/cancel API)
```

mule launches an agent TUI inside an inner PTY and passes through all I/O to the outer terminal — you see the full TUI exactly as if you ran the agent directly. Simultaneously, mule feeds the agent's output through a VT emulator to track screen state internally.

A Unix socket API lets orchestrators (like [orca](https://github.com/weill-labs/orca)) submit prompts, query status, and cancel work — without fragile send-keys heuristics. The driver knows each agent's TUI intimately: what idle looks like, how to submit input, when work is complete.

## Usage

```bash
# Run codex with a control socket
mule codex --socket /tmp/mule-task.sock

# Submit a prompt (from another process)
echo '{"method":"submit","params":{"prompt":"Fix the auth bug. TDD."}}' \
  | socat - UNIX:/tmp/mule-task.sock

# Query status
echo '{"method":"status"}' | socat - UNIX:/tmp/mule-task.sock
# → {"state":"working","elapsed":"12s"}
```

## Drivers

Each agent gets a dedicated driver — a state machine with tested patterns for that specific TUI.

| Agent | Status |
|-------|--------|
| Codex | In progress |
| Claude Code | Planned |

## Design

- **Visual passthrough.** All agent I/O flows to the terminal. The TUI looks identical to running the agent directly. Someone watching can't tell the difference.
- **Internal screen awareness.** A VT emulator ([weill-labs/x/vt](https://github.com/weill-labs/x/vt)) parses agent output in parallel, giving the driver structured access to screen state without external screen scraping.
- **Per-agent state machines.** Each driver defines states (starting, idle, working, complete), transition patterns, and input sequences. Tested against real agent sessions.
- **Unix socket API.** JSON messages over a Unix socket. Simple, language-agnostic, no HTTP overhead.

## Building

```bash
go build -o ~/.local/bin/mule .
```
