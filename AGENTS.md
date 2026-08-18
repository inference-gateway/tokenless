# Agent & Contributor Guide

## Project Structure

```
./
├── cmd/tokenless/          # Standalone binary entry point
├── examples/               # Runnable examples (separate Go module)
│   ├── 00-cobra-agent/     # End-to-end cobra CLI example with tests
│   └── ...                  # Numbered subdirectories for each example
├── gateway/                # Core library: HTTP server, scenario engine, wire types
├── .agents/skills/         # Agent skills (e.g. tokenless skill for testing)
├── .github/workflows/      # CI and task automation
├── AGENTS.md               # This file
├── go.mod / go.sum         # Root module (Go 1.26.4)
└── README.md               # Project overview and usage
```

**Packages:**

- `gateway` — the HTTP server, scenario parser/validator (`Load`, `LoadFile`, `Default`), hand-written wire types, and the embedded default scenario library. Zero dependencies beyond `gopkg.in/yaml.v3`.
- `tokenless` (root) — Go test helpers: `BuildBinary` (build once in `TestMain`), `Orchestrator`/`Orchestrator.Run` (hermetic subprocess runs), `JSONLines`/`ContentsByRole`/`StatusOfType` (NDJSON assertions), tmux TUI drivers (`SendKeys`, `CapturePane`, `WaitForPane`), and `ToolLoop` for multi-turn tool-invocation tests.
- `cmd/tokenless` — the standalone binary.

## Build, Test, and Dev Commands

This project uses plain Go tooling. A convenience `Taskfile.yml` is also available for common commands when `task` is installed (`go install github.com/go-task/task/v3/cmd/task@latest`).

```bash
# Build everything
go build ./...

# Run all tests (root module)
go test ./...

# Run tests with race detector
go test -race ./...

# Run tests in the examples module too
(cd examples && go test ./...)

# Format check (CI enforces this)
gofmt -l .          # list unformatted files
gofmt -w .          # fix formatting

# Static analysis
go vet ./...

# Full CI check (what CI runs)
gofmt -l . && test -z "$(gofmt -l .)"
go vet ./...
go test ./...
(cd examples && go vet ./... && go test ./...)

# Run the standalone binary
go run ./cmd/tokenless --port 8080 --scenarios gateway/scenarios.yaml
```

## Coding Style

- **Language:** Go 1.26.4, idiomatic standard library usage.
- **Formatting:** `gofmt` — no exceptions. CI rejects unformatted code.
- **Imports:** Standard library first, then third-party, grouped by blank line.
- **Testing:** Use `github.com/stretchr/testify/require` for assertions. Table-driven tests with `t.Run` subtrees. Test helpers use `t.Helper()`.
- **Wire types:** Hand-written, minimal — only fields the mock reads or writes. JSON tags follow OpenAI/Anthropic wire formats plus gateway extensions.
- **Error handling:** Return errors from internal functions; `log.Fatal` only in `main()`. Panic only in `Default()` when embedded scenarios are invalid (a build-time invariant).
- **Concurrency:** `sync.Mutex` guards mutable state (failure counters, request recordings). No channels or atomics unless needed.
- **Naming:** Exported names are PascalCase; unexported helpers are camelCase. Acronyms are uppercase (`HTTP`, `JSON`, `URL`).
- **Comments:** Package-level doc comments on every package. Exported types and functions get doc comments. Unexported helpers get inline comments where non-obvious.

## Commit and PR Conventions

**Commits** follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`.
Scope is the package or area (e.g. `gateway`, `tokenless`, `cmd/tokenless`).

Examples:
- `feat(gateway): add Anthropic /v1/messages endpoint`
- `fix(tokenless): handle empty stdout in JSONLines`
- `docs: add scenario format examples`

**Pull Requests:**

- Open as **draft** early, mark **ready** when CI passes.
- Title follows the same Conventional Commits format.
- Description includes a **Summary** (2–4 sentences) and a **Changes** bullet list.
- A human reviews and merges — do not self-merge.

## Pre-commit Hook

This repo ships a `.githooks/pre-commit` hook that runs `gofmt`, `go vet`, and `go test` before every commit. To activate it:

```bash
git config core.hooksPath .githooks
```

The hook is inert until you run that command. Once set, `git commit` will refuse if formatting, vet, or tests fail. The hook runs against the root module only (not `examples/`), keeping it fast for day-to-day work.

**Agents:** run `git config core.hooksPath .githooks` at the start of every task to activate the pre-commit hook.

## Agent Skills

The `.agents/skills/` directory contains reusable skill definitions for agent workflows. The `tokenless` skill documents how to test LLM agents against the mock gateway. Skills are loaded by the agent runtime and provide structured instructions for common tasks.
