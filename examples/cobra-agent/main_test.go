// The harness in action: build the cobra binary once, run it as a real
// subprocess against the mock, and assert on its stdout - the same pattern
// scales from this toy to a full agent CLI.
package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
	gateway "github.com/inference-gateway/tokenless/gateway"
)

var binPath string

func TestMain(m *testing.M) {
	var cleanup func()
	var err error
	binPath, cleanup, err = tokenless.BuildBinary(".", "COBRA_AGENT_E2E_BINARY")
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func app(t *testing.T, gatewayURL string) tokenless.Orchestrator {
	t.Helper()
	return tokenless.Orchestrator{Bin: binPath, Env: map[string]string{"AGENT_GATEWAY_URL": gatewayURL + "/v1"}}
}

func TestAskAnswersFromScenario(t *testing.T) {
	mock := tokenless.StartMock(t)

	res := app(t, mock.URL).Run(t, "ask", "say hello")
	
	require.Zero(t, res.ExitCode, "stderr: %s", res.Stderr)
	require.Equal(t, "Hello! How can I help?", strings.TrimSpace(res.Stdout))
}

func TestAskWithCustomScenarios(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: meaning
    match: '(?i)meaning of life'
    turns:
      - content: "42."
`))
	require.NoError(t, err)
	mock := tokenless.StartMock(t, defs)
	
	res := app(t, mock.URL).Run(t, "ask", "what is the meaning of life?")

	require.Zero(t, res.ExitCode, "stderr: %s", res.Stderr)
	require.Equal(t, "42.", strings.TrimSpace(res.Stdout))
}

func TestAskFailsWithoutGatewayURL(t *testing.T) {
	res := tokenless.Orchestrator{Bin: binPath}.Run(t, "ask", "say hello")

	require.NotZero(t, res.ExitCode)
	require.Contains(t, res.Stderr, "AGENT_GATEWAY_URL")
}
