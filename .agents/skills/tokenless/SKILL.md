---
name: tokenless
description: >
  Test LLM agents against a deterministic mock gateway with zero token cost.
  Use when writing or debugging e2e/integration tests for agents built on the
  OpenAI-compatible or Anthropic Messages APIs: author scenarios.yaml scripted
  conversations, embed the gateway in Go tests via the harness package, run the
  standalone tokenless binary, or drive an app's own mock switch
  (e.g. INFER_GATEWAY_MOCK).
license: Apache-2.0
---

# Tokenless

Deterministic mock LLM gateway (`github.com/inference-gateway/tokenless`).
You script conversations in YAML; a mock HTTP server serves them over both the
OpenAI-compatible API and Anthropic's Messages API. The agent under test runs
its real tool/approval/output pipeline against a "model" that says exactly what
the test expects — no API keys, no tokens, no nondeterminism.

Endpoints: `POST /v1/chat/completions` (sync + SSE), `POST /v1/messages`
(Anthropic SSE), `POST /v1/images/generations` and `/v1/images/edits` (canned
1x1 PNG), `GET /v1/models`, `GET /v1/health`.

## scenarios.yaml

```yaml
# Optional: custom model list for GET /v1/models (omit for built-in list)
models:
  - id: my-custom-model
    object: model
    owned_by: me

fallback:
  content: "Done." # served when no scenario matches or turns run out
scenarios:
  - name: write-approved # required, unique
    match: '(?i)create a file named approved\.txt' # Go regex, unanchored
    model: gpt-4o-mini # optional: only match requests for this model
    turns:
      - tool_calls:
          - { name: Write, args: { file_path: "approved.txt", content: "hi" } }
      - content: "The file was written."
```

Resolution is stateless: the scenario is chosen by matching `match` against the
latest real user message (`<system-reminder>` blocks and background-job notices
are skipped; first match wins), and the turn index is the count of assistant
messages already in the request. Scenarios may also set `model` to constrain
matching to a specific request model (exact string match; unset = no constraint).
Turn fields:

| Field        | Meaning                                                                          |
| ------------ | -------------------------------------------------------------------------------- |
| `content`    | Assistant text                                                                    |
| `reasoning`  | Reasoning/thinking text                                                           |
| `tool_calls` | `[{name, args}]` — args is an arbitrary object                                    |
| `usage`      | `{prompt_tokens, completion_tokens, cached_tokens, cache_write_tokens}`           |
| `chunk_size` | SSE chunk size in runes (default 16)                                              |
| `delay_ms`   | Delay between chunks                                                              |
| `error`      | `{status, times}` — status one of 408/429/500/502/503/504; `times: -1` = forever |
| `stall`      | `{times, connect}` — hang the response (or the connect) to test timeouts          |
| `malformed`  | Emit invalid JSON to test parse-error handling                                    |

A commented reference file lives at `examples/scenarios.yaml`; the built-in
library is `gateway/scenarios.yaml`.

## Go tests: the tokenless package

`github.com/inference-gateway/tokenless` — the full worked example is
`examples/cobra-agent/`.

```go
func TestMain(m *testing.M) {
    bin, cleanup, err := tokenless.BuildBinary(repoRoot(), "MYAPP_E2E_BINARY") // env var reuses a prebuilt binary
    ...
}

func TestApprovedWrite(t *testing.T) {
    mock := tokenless.StartMock(t) // built-in scenarios; pass gateway.LoadFile(...) results for your own
    res := tokenless.Orchestrator{Bin: bin, Dir: t.TempDir(), Env: map[string]string{"MYAPP_GATEWAY_URL": mock.URL}}.
        Run(t, "run", "--prompt", "create a file named approved.txt")
    // res.Stdout, res.Stderr, res.ExitCode
    mock.AssertExpectations(t)
}
```

`Orchestrator.Run` executes the binary as a subprocess with a hermetic env (temp HOME).
NDJSON output helpers: `JSONLines(t, stdout)`, `ContentsByRole(lines, role)`,
`StatusOfType(lines, typ)`, `ToolMessages(body)`, `WriteFixtures(t, dir, names...)`.
tmux TUI drivers: `CapturePane(session)`, `SendKeys(t, session, keys...)`,
`WaitForPane(t, session, want, timeout)`.

## Gateway package directly

```go
defs, _ := gateway.LoadFile("scenarios.yaml") // or gateway.Load(bytes), gateway.Default()
srv := gateway.New(defs)                      // http.Handler; no args = built-in library
// after the test: srv.Requests() returns []Recorded for asserting what the agent sent
```

Model constants: `gateway.DefaultModel` (`openai/gpt-4o`), `gateway.AnthropicModel`
(`anthropic/claude-sonnet-4-5`), `gateway.ImageModel` (`openai/gpt-image-2`).

## Standalone binary

```sh
go run github.com/inference-gateway/tokenless/cmd/tokenless --port 0 --scenarios scenarios.yaml
```

Flags: `--host`, `--port` (0 = free port, printed on start), `--scenarios`
(default `$TOKENLESS_SCENARIOS`, then built-in), `--model`. Point any
OpenAI/Anthropic client or non-Go test suite at the printed URL —
`examples/clients/` has OpenAI, Anthropic, and inference-gateway SDK client
programs.

## Consumer mock switches

Apps may embed tokenless behind their own flag. The `infer` CLI does this with
`INFER_GATEWAY_MOCK=true` and `INFER_GATEWAY_MOCK_SCENARIOS=<path>` — those env
vars are owned by the CLI, not by tokenless itself. When testing such an app,
set its switch instead of running the standalone binary.
