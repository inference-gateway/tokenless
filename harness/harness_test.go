package harness_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gateway "github.com/inference-gateway/tokenless/gateway"
	harness "github.com/inference-gateway/tokenless/harness"
)

// TestAppRun doubles as the usage example for App: name the binary, hand it
// env/stdin, get a Result back.
func TestAppRun(t *testing.T) {
	app := harness.App{
		Bin:   "/bin/sh",
		Env:   map[string]string{"GREETING": "hello from tokenless"},
		Stdin: "and stdin too",
	}

	res := app.Run(t, "-c", `printf '%s / ' "$GREETING"; cat; echo oops 1>&2; exit 3`)

	require.Equal(t, "hello from tokenless / and stdin too", res.Stdout)
	require.Equal(t, "oops\n", res.Stderr)
	require.Equal(t, 3, res.ExitCode)
}

// TestStartMockWithScenarios is the end-to-end shape of an agent test: mock
// with app-owned scenarios, drive the "app" (curl here), assert on the
// recorded request.
func TestStartMockWithScenarios(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: greet
    match: '(?i)^say hello'
    turns:
      - content: "Hello from the scenario."
`))
	require.NoError(t, err)

	gw, url := harness.StartMock(t, defs)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"say hello"}]}`
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	var out gateway.CreateChatCompletionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	_ = resp.Body.Close()

	require.Equal(t, "Hello from the scenario.", out.Choices[0].Message.Content.Text())
	require.Equal(t, "greet", gw.Requests()[0].Scenario)
}

// TestJSONLines shows the NDJSON assertion helpers on a headless agent's
// stdout contract.
func TestJSONLines(t *testing.T) {
	stdout := `{"role":"assistant","content":"hi"}
{"type":"session_stats","requests":1}
`
	lines := harness.JSONLines(t, stdout)

	require.Equal(t, []string{"hi"}, harness.ContentsByRole(lines, "assistant"))
	require.NotNil(t, harness.StatusOfType(lines, "session_stats"))
	require.Nil(t, harness.StatusOfType(lines, "agent_error"))
}
