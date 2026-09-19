<h1 align="center">Tokenless</h1>

<p align="center">
  <!-- CI Status Badge -->
  <a href="https://github.com/inference-gateway/tokenless/actions/workflows/ci.yml?query=branch%3Amain">
    <img
      src="https://github.com/inference-gateway/tokenless/actions/workflows/ci.yml/badge.svg?branch=main"
      alt="CI Status"/>
  </a>
  <!-- Version Badge -->
  <a href="https://github.com/inference-gateway/tokenless/releases">
    <img src="https://img.shields.io/github/v/tag/inference-gateway/tokenless?color=blue&style=flat-square"
         alt="Version"/>
  </a>
  <!-- License Badge -->
  <a href="https://github.com/inference-gateway/tokenless/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/inference-gateway/tokenless?color=blue&style=flat-square" alt="License"/>
  </a>
  <!-- Go Version Badge -->
  <a href="https://github.com/inference-gateway/tokenless/blob/main/go.mod">
    <img src="https://img.shields.io/github/go-mod/go-version/inference-gateway/tokenless?color=blue&style=flat-square&logo=go" alt="Go Version"/>
  </a>
</p>

Tokenless is a deterministic mock LLM gateway for testing agents without API
calls, token costs, or unpredictable model responses. Define predictable
conversations in YAML and run them through a mock server that speaks the
OpenAI-compatible API and Anthropic's Messages API - your agent runs its real
tools, real approval flows, and real output pipeline against a "model" that
always says exactly what your test expects.

- [Key Features](#key-features)
- [How It Works](#how-it-works)
- [Supported Endpoints](#supported-endpoints)
- [Usage](#usage)
- [Scenario Format](#scenario-format)
- [Packages](#packages)
- [Alternatives](#alternatives)
- [Contributing](#contributing)
- [License](#license)

## Key Features

- 📜 **Open Source**: Available under the Apache 2.0 License.
- 🎯 **Deterministic**: Scenarios are selected by regex and served by turn
  position - no hidden session state, no cleverness.
- 🌊 **Real Streaming**: OpenAI SSE chunks and Anthropic-native event streams,
  including multi-fragment tool-call arguments.
- 🔧 **Tool-Call Scripting**: Script tool calls turn by turn and exercise your
  agent's real execution and approval paths.
- 💥 **Failure Injection**: HTTP errors, stalled streams, and malformed SSE
  frames for the retry paths a real provider never tests.
- 🧰 **Three Consumption Modes**: Go import, standalone binary, or an app-level
  mock switch - any language, any harness.
- 💸 **Zero Cost**: A scenario costs the same to run one time or one million
  times: nothing.

## How It Works

It is not a stub that returns `"Hello, world"`. It is a scenario engine:
a scenario is selected by regex against the first user message, and each turn
is served by position - derived from how many assistant messages are already
in the request.

```yaml
# Optional: custom model list for GET /v1/models (omit for built-in list)
models:
  - id: my-custom-model
    object: model
    owned_by: me

fallback:
  content: "Done."

scenarios:
  - name: write-approved
    match: '(?i)create a file named approved\.txt'
    turns:
      - tool_calls:
          - { name: Write, args: { file_path: "approved.txt", content: "hi" } }
      - content: "The file was written."

  - name: model-specific
    match: '(?i)model specific'
    model: gpt-4o-mini  # optional: only match requests for this model
    turns:
      - content: "This is a model-specific response for gpt-4o-mini."
```

## Supported Endpoints

| Endpoint                                              | Notes                                                                                                      |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `POST /v1/chat/completions`                           | Sync JSON and SSE streaming; text, reasoning deltas, multi-fragment tool-call chunks, usage frame, `[DONE]` |
| `POST /v1/messages`                                   | Anthropic-native: thinking/text/tool_use blocks, `input_json_delta` fragments, cache-aware usage            |
| `POST /v1/images/generations`, `POST /v1/images/edits`| Canned 1x1 PNG so decode-and-save paths run end to end                                                     |
| `GET /v1/models`                                      | Model list with pricing and context-window metadata                                                        |
| `GET /v1/expect`                                      | JSON report of recorded expectation failures; `200` when clean, `412` otherwise |
| `GET /v1/health`                                      | `{"status":"ok"}`                                                                                          |

## Usage

**1. Go import** - embed the mock in your tests:

```go
import "github.com/inference-gateway/tokenless"

func TestSomething(t *testing.T) {
    mock := tokenless.StartMock(t)
    orc := tokenless.Orchestrator{Bin: binPath, Env: map[string]string{"MYAPP_GATEWAY_URL": mock.URL}}

    res := orc.Run(t, "agent", "say hello")
    require.Zero(t, res.ExitCode)
    mock.AssertExpectations(t)
    ...
}
```

**2. Standalone binary** - for any agent in any language; point its base URL
at the mock:

```bash
go run github.com/inference-gateway/tokenless/cmd/tokenless@latest \
  --port 8080 --scenarios scenarios.yaml
# or: TOKENLESS_SCENARIOS=scenarios.yaml tokenless --port 8080
```

**3. Via your app's own mock switch** - an app can embed tokenless and expose
it behind an environment variable, so anything that spawns the app gets the
mock without writing a line of Go. The
[infer CLI](https://github.com/inference-gateway/cli) does exactly this:

```bash
INFER_GATEWAY_MOCK=true \
INFER_GATEWAY_MOCK_SCENARIOS=$PWD/e2e/scenarios.yaml \
infer agent --require-approval -m openai/gpt-4o "create a file named approved.txt"
```

That is how the inference-gateway Desktop app runs its UI tests: its repo
ships its own `scenarios.yaml`, and the spawned `infer` children inherit both
variables. Yesterday this tested a CLI, today a desktop app, tomorrow your
thing.

**Already have an app built on an official client SDK?** Nothing changes in
your code - construct the client with its base URL pointed at the mock. See
[examples](examples) for runnable programs using the
[official OpenAI Go client](examples/01-openai-client/main_test.go) (sync and
streaming) and the
[inference-gateway Go SDK](examples/03-sdk-client/main_test.go).

**Testing a CLI end to end?**
[examples/00-cobra-agent](examples/00-cobra-agent) is a complete worked example: a
small [cobra](https://github.com/spf13/cobra) CLI whose gateway URL comes from
an environment variable, and a [test](examples/00-cobra-agent/main_test.go) that
builds the real binary once (`tokenless.BuildBinary` in `TestMain`), starts the
mock (`tokenless.StartMock`), runs the binary as a subprocess
(`tokenless.Orchestrator{...}.Run`), and asserts on its stdout and exit code - including
a custom inline scenario and a failure path. The examples live in their own Go
module, so none of their dependencies touch the library.

## Scenario Format

See [examples/scenarios.yaml](examples/scenarios.yaml) for a commented example
and [gateway/scenarios.yaml](gateway/scenarios.yaml) for the embedded default
library. When a scenario runs out of turns the top-level `fallback` is served,
which lets a headless agent terminate naturally. Runnable Go examples live in
[gateway/example_test.go](gateway/example_test.go).

### Expect blocks

Each turn can carry an `expect` block that validates the incoming request that
resolved to that turn. A mismatch is recorded but the turn is still served as
normal, keeping the conversation on script.

```yaml
turns:
  - tool_calls:
      - { name: Write, args: { file_path: "hi.txt", content: "hi" } }
  - expect:
      model: gpt-4o
      endpoint: /v1/chat/completions
      tool_calls:
        - name: Write
          args: { file_path: "hi.txt" }
      messages:
        - role: tool
          content: "fixture content"
    content: "The file was written."
```

Supported `expect` fields:

| Field        | Matching rule                                                                 |
| ------------ | ----------------------------------------------------------------------------- |
| `model`      | Exact string match against the request model                                  |
| `endpoint`   | Exact string match against the request path                                   |
| `messages`   | Ordered subsequence: each expected message must appear in order, with exact `role` and substring `content`; extra messages allowed |
| `tool_calls` | Ordered, deep partial match on `args`: expected keys must be present and equal, extra keys allowed |

Failures are surfaced three ways:
- `Mock.ExpectFailures() []ExpectFailure` in Go
- `Mock.AssertExpectations(t)` with readable want/got diffs
- `GET /v1/expect` returns a JSON report (200 when clean, 412 otherwise)
- `X-Tokenless-Expect-Failure` header on the mismatched response

## Packages

- `tokenless` (root) - Go test helpers: `StartMock` (returns `*Mock` with `URL`
  and `AssertExpectations`), `Orchestrator`/`Orchestrator.Run` (hermetic
  subprocess runs with caller-supplied env), `BuildBinary` (build once in
  TestMain), `JSONLines`/`ContentsByRole`/`StatusOfType` (NDJSON assertions),
  and tmux TUI drivers (`SendKeys`, `CapturePane`, `WaitForPane`).
- `gateway` - the HTTP server, scenario parser/validator (`Load`, `LoadFile`,
  `Default`), the hand-written wire types, and the embedded default scenario
  library. Zero dependencies beyond yaml.
- `cmd/tokenless` - the standalone binary.

## Alternatives

If you want TypeScript-native record-and-replay across many providers, look
at [aimock](https://github.com/CopilotKit/aimock); for OpenAI-only JMESPath
rules in Node, [mock-llm](https://github.com/dwmkerr/mock-llm). Tokenless is
the Go-embeddable, single-YAML, regex-scenario take on the same problem -
built for hermetic `go test` runs and for harnesses that spawn agents as
subprocesses.

## Contributing

Contributions are welcome. Open an issue or a pull request on
[GitHub](https://github.com/inference-gateway/tokenless).

## License

This project is licensed under the Apache 2.0 License - see the
[LICENSE](LICENSE) file for details.
