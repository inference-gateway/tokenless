// Testing real tool invocation: the ToolLoop drives a multi-turn conversation
// against the mock, executing registered Go tool funcs (or exec-based tools
// from the scenario spec) when the mock returns tool_calls, and feeding the
// real output back as tool results.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
	gateway "github.com/inference-gateway/tokenless/gateway"
)

// TestGoTools registers real Go tool implementations in the ToolLoop.
// The mock returns scripted tool_calls; ToolLoop.Run executes the real
// read_file func and feeds the real file content back as the tool result.
func TestGoTools(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: summarize-config
    match: '(?i)summarize.*config'
    turns:
      - tool_calls:
          - { name: read_file, args: { path: "config.yaml" } }
      - content: "Your config uses port 8080."
`))
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("port: 8080\n"), 0o600))
	t.Chdir(dir)

	mock := tokenless.StartMock(t, defs)

	loop := &tokenless.ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
		Tools: map[string]tokenless.ToolFunc{
			"read_file": func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", err
				}
				b, err := os.ReadFile(a.Path)
				return string(b), err
			},
		},
	}

	result := loop.Run(t, "summarize config.yaml")
	require.Contains(t, result.FinalContent, "port 8080")
	mock.AssertExpectations(t)
}

// TestExecTools loads exec-based tool definitions from the scenario file
// via LoadScenarioTools. Each tool runs as a subprocess with its argv
// templated from the tool call's JSON args.
func TestExecTools(t *testing.T) {
	defs, err := gateway.Load([]byte(`
tools:
  read_file:
    exec: ["cat", "{{.path}}"]
fallback:
  content: "Done."
scenarios:
  - name: summarize-config
    match: '(?i)summarize.*config'
    turns:
      - tool_calls:
          - { name: read_file, args: { path: "config.yaml" } }
      - content: "Your config uses port 8080."
`))
	require.NoError(t, err)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("port: 8080\n"), 0o600))

	mock := tokenless.StartMock(t, defs)

	// Inject the mock into the context so the tool function can detect
	// it is running in a test and skip real external calls.
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, mock)

	loop := &tokenless.ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
		Context: ctx,
	}
	loop.LoadScenarioTools(defs)

	// Override the tool to demonstrate the API-skip pattern: a tool that
	// would normally make an external API call, but in tests returns
	// canned data when the mock is detected in the context.
	loop.Tools["read_file"] = func(ctx context.Context, args json.RawMessage) (string, error) {
		var a struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}

		// In tests, skip the real API call and return test data.
		if _, ok := ctx.Value(ctxKey{}).(*tokenless.Mock); ok {
			return "port: 8080\n", nil
		}

		// Real implementation: would make an HTTP API call here.
		b, err := os.ReadFile(a.Path)
		return string(b), err
	}

	result := loop.Run(t, "summarize config.yaml")
	require.Contains(t, result.FinalContent, "port 8080")
	mock.AssertExpectations(t)
}
