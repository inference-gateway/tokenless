# AGENTS.md

Tokenless is a deterministic mock LLM gateway for testing agents without API calls or token cost: conversations are scripted in YAML scenarios, selected by regex against the first user message, and served turn by turn. Two modules: the root (`github.com/inference-gateway/tokenless`) and `examples/` (separate module, `replace => ../`).

## Packages

- `gateway/` — HTTP server (`Server`, `New`), scenario parser/validator (`Load`, `LoadFile`, `Default`), hand-written wire types (`types.go`, `messages.go`), embedded default scenario library (`scenarios.yaml`). Only dependency: `gopkg.in/yaml.v3` — keep it that way, this package is imported into other projects' test suites.
- Root package (`tokenless`) — Go test helpers: `StartMock` (httptest server + `AssertExpectations`), `Orchestrator`/`Run` (hermetic subprocess runs with caller-supplied env), `BuildBinary` (build once in `TestMain`), NDJSON assertions (`JSONLines`, `ContentsByRole`, `StatusOfType`), tmux TUI drivers (`SendKeys`, `CapturePane`, `WaitForPane`), and `ToolLoop` (drives real tool functions against the mock, with an optional per-call `Approve` callback).
- `cmd/tokenless` — standalone binary (`--host`, `--port`, `--model`, `--scenarios`; falls back to `$TOKENLESS_SCENARIOS`, then the built-in library). First stdout line is the readiness line `tokenless listening on http://...`; one request log line per call on stderr.
- `examples/00-…`–`10-…` — numbered, runnable examples; most are exercised by their `main_test.go`.
- `.agents/skills/tokenless/SKILL.md` — instructions for testing agents against the mock.

## Build, test, lint

The root module has a Taskfile (`task` runs fmt, vet, build, test; `task test` adds `-race`; install `task` via `go install github.com/go-task/task/v3/cmd/task@latest`). CI (`.github/workflows/ci.yml`) runs exactly:

```bash
gofmt -l . && test -z "$(gofmt -l .)"
go vet ./...
go test ./...
(cd examples && go vet ./... && go test ./...)
```

Try the binary: `go run ./cmd/tokenless --port 8080 --scenarios gateway/scenarios.yaml`

## Code style

- Go 1.26.7; `gofmt` with no exceptions; `go vet` clean.
- testify `require` for assertions; table-driven tests with `t.Run`; helpers call `t.Helper()`.
- Wire types are hand-written and minimal — only fields the mock reads or writes; JSON tags follow the OpenAI/Anthropic wire formats plus gateway extensions.
- Return errors from internal functions; `log.Fatal` only in `main()`; panic only in `gateway.Default()` when embedded scenarios are invalid (a build-time invariant).
- `sync.Mutex` guards mutable state (failure counters, request recordings). No channels or atomics unless needed.

## Scenario engine gotchas

Scenario selection matches the regex against the first user message only; turns are served by position; the top-level `fallback` serves when turns run out. `expect` blocks validate requests without breaking the flow — assert with `Mock.AssertExpectations(t)` or `GET /v1/expect`. Failure injection (`error`, `stall`, `malformed`) is configured per turn.

## Commits & PRs

Releases are automated by semantic-release from commit history, so commits must follow [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <description>` with scope = area (`gateway`, `tool_loop`, `tokenless`, `cmd/tokenless`). PRs: open as draft, mark ready when CI passes; a human merges — do not self-merge.

Activate the pre-commit hook at the start of every task:

```bash
git config core.hooksPath .githooks
```

It runs gofmt, vet, and tests (root module only) before each commit.