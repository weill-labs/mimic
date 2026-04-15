# CLAUDE.md

## Project tracking

Linear project (mimic): https://linear.app/weill-labs/project/mimic-483188e383b7
Linear project (mule, legacy): https://linear.app/weill-labs/project/mule-0f918bb42136

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
type Submission struct {
    Body []byte
    Submit []byte
    KeyDelay time.Duration
    SettleDelay time.Duration
}

type Driver interface {
    // DetectState reads the current screen and returns the agent's state.
    DetectState(screen Screen) State
    // SubmitPrompt returns the prompt body, submit bytes, and timing hints.
    SubmitPrompt(prompt string) Submission
    // CancelWork returns the key sequence to cancel in-progress work.
    CancelWork() []byte
}
```

## Development

### Build And Test

```bash
make setup                         # activate repo git hooks
make install                       # install mimic to ~/.local/bin/
go test ./... -timeout 120s        # run all tests
make coverage                      # test coverage (use this, not go test -coverprofile)
```

### Confirm Before Any Destructive Pane or Daemon Action

**Get explicit user approval before** killing panes, closing windows, restarting orca, cancelling tasks, or any action that destroys worker state. No exceptions. These actions lose running agent context, in-progress work, and session history that cannot be recovered.

Destructive actions that require user confirmation:

- **Killing or closing a pane** (`amux kill`, closing a window)
- **Restarting orca** (`orca stop`, `orca start`) — kills worker panes and orphans active tasks
- **Cancelling an orca task** (`orca cancel`)
- **Killing a process** in a worker pane — capture diagnostics first (`kill -3` / SIGQUIT for Go goroutine trace). The trace is the only evidence for deadlock root cause and is destroyed on kill.
- **`/exit` on codex** — run postmortem first. Session context (reasoning, corrections, decisions) is destroyed on exit and cannot be recovered.
- **Mass `send-keys`** — if sending to more than 3 panes, present the message and pane list to the user for approval. Batch operations are where mistakes scale.

When troubleshooting spawn or assignment failures, fix the specific problem (clean up duplicate panes, check amux state) rather than restarting orca.

- **Recovery ≠ assignment**: restoring a codex process is a separate action from giving it work. After recovery, leave workers at idle prompts. Only assign specific, user-approved issues.

### TDD Workflow

All development follows red-green-refactor with **separate commits** for each phase:

1. **Red** -- Write failing tests. Commit them alone. Confirm they fail for the right reason (missing feature, not a syntax error).
2. **Green** -- Minimal production code to make tests pass. Commit separately.
3. **Refactor** -- Simplify, extract helpers, remove duplication. Commit separately.

### Test Philosophy

Tests should read like specs. Minimize logic in assertions so a human can read the test and immediately understand what behavior is expected. Use table-driven tests for unit tests with multiple cases -- define a `tests` slice of structs, iterate with `t.Run(tt.name, ...)`, and call `t.Parallel()` in each subtest.

Driver tests must exercise real agent binaries in real PTYs. Use recorded VT output for unit tests of state detection. Integration tests spawn the actual agent and verify the full submit/complete cycle.

When a change adds a new test or modifies an existing test, run that targeted test slice with `-count=100` before calling the work done. Treat any failure in those repeated runs as a flake to investigate, not as an acceptable one-off.

**Fix flaky tests by finding the root cause.** Never make tests serial to avoid a flake — that hides the bug (shared state, resource contention, missing synchronization) instead of fixing it. If tests deadlock under `-count=3 -parallel=2`, the fix is in the code, not the test runner flags.

**Inject dependencies, do not add package-level `var` for test seams.** When production code needs a swappable dependency, pass it as a function parameter or struct field -- never as a mutable package-level `var`. Tests pass stubs directly; production call sites pass the real implementation. This keeps tests parallel and eliminates shared mutable state.

### Pre-Push Rebase

Rebase onto `origin/main` before the first push (`git fetch origin main && git rebase origin/main`). Multiple features often land in parallel; rebasing before push avoids repeated merge conflict resolution after the PR is open.

Do not `git pull` a dirty local `main`. If `main` has uncommitted work, leave it alone and start the next change from a fresh branch based on `origin/main` instead. Do not use `git worktree` unless the user explicitly asks for it.

If a PR is already open and `git fetch origin main` or `git pull` advances `origin/main`, refresh that PR branch onto `origin/main` before treating it as current again. After the refresh, rerun verification on the rebased branch before pushing.

### PR Title And Description

PR title and description are the permanent record of why a change was made. Write them for a reviewer seeing the diff for the first time.

**Title**: State what changed in imperative mood, under 70 characters. Omit ticket prefixes like `LAB-314:` — link tickets in the description body instead.

**Description** must include four sections:

1. **Motivation** -- Why this change? What broke, what was missing, or what user need does it address? One to three sentences.
2. **Summary** -- What changed? Bullet the key changes. Describe the PR as a complete unit, not per-commit.
3. **Testing** -- How was it verified? Include the exact test commands a reviewer can copy-paste.
4. **Review focus** -- What should reviewers look at? Call out non-obvious design decisions, edge cases, or areas where you are least confident.

Use matter-of-fact language. State what the PR does, not how good it is. Avoid vague qualifiers like "robust", "comprehensive", "elegant", or "production-ready". If a Linear issue exists, add `Closes LAB-NNN` at the bottom.

### Review Before Done

After creating or updating a PR, run a review pass and a simplification pass before considering the work done. Claude Code gets hook reminders for this. Codex users should use the repo PR workflow skill or perform the steps explicitly.

If a change in this repo is ready for review, open the PR proactively instead of asking whether to make one.

### Merge Conflict Resolution

After resolving merge conflicts, run `go vet ./...` locally before committing. Git auto-merge can silently produce duplicate declarations (e.g., methods defined in both sides) that compile but fail vet.

### Verify Mergeability Before Declaring PRs Ready

Before telling the user a PR is safe to merge, check for merge conflicts with main: `git fetch origin main && git merge-tree --write-tree origin/main origin/BRANCH`. If there are conflicts, rebase the branch onto `origin/main` and resolve them before declaring ready.

### Merge Policy

GitHub PRs for this repo are squash-only. `gh pr merge --merge` and `gh pr merge --rebase` will fail.

After merging, verify local state explicitly: check that the checkout is on `main`, the worktree is clean, and `HEAD` matches `origin/main`.

After merging, explicitly run `$postmortem`. The post-merge hook prompts for this automatically.

### User Handoffs

Before stopping to wait for user input, suggest the next concrete action the user should take or approve. Do not end at "waiting on you" without a specific next step.

### Include Baseline Numbers In Performance PRs

When creating PRs that add or modify benchmarks, include a `Baseline numbers` section in the PR description with representative results in a markdown table.

## Issue Tracking

File bugs and feature requests in the [`mimic` project](https://linear.app/weill-labs/project/mimic-483188e383b7). GitHub Issues is not actively monitored.
