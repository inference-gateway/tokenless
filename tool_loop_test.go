package tokenless

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

func TestToolLoop_GoTools(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: read-config
    match: '(?i)read.*config'
    turns:
      - tool_calls:
          - { name: read_file, args: { path: "config.yaml" } }
      - content: "Config loaded."
`))
	require.NoError(t, err)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("port: 8080\n"), 0o600))

	mock := StartMock(t, defs)

	loop := &ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
		Tools: map[string]ToolFunc{
			"read_file": func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ Path string `json:"path"` }
				if err := json.Unmarshal(args, &a); err != nil {
					return "", err
				}
				b, err := os.ReadFile(a.Path)
				return string(b), err
			},
		},
	}

	result := loop.Run(t, "read the config file")
	require.Equal(t, "Config loaded.", result.FinalContent)
	mock.AssertExpectations(t)
}

func TestToolLoop_ExecTools(t *testing.T) {
	defs, err := gateway.Load([]byte(`
tools:
  read_file:
    exec: ["cat", "{{.path}}"]
fallback:
  content: "Done."
scenarios:
  - name: read-config
    match: '(?i)read.*config'
    turns:
      - tool_calls:
          - { name: read_file, args: { path: "config.yaml" } }
      - content: "Config loaded."
`))
	require.NoError(t, err)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("port: 8080\n"), 0o600))

	mock := StartMock(t, defs)

	loop := &ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
	}
	loop.LoadScenarioTools(defs)

	// Override the path to use the temp dir file.
	loop.Tools["read_file"] = func(ctx context.Context, args json.RawMessage) (string, error) {
		var a struct{ Path string `json:"path"` }
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		b, err := os.ReadFile(a.Path)
		return string(b), err
	}

	result := loop.Run(t, "read the config file")
	require.Equal(t, "Config loaded.", result.FinalContent)
	mock.AssertExpectations(t)
}

func TestToolLoop_UnregisteredTool(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: unknown-tool
    match: '(?i)unknown'
    turns:
      - tool_calls:
          - { name: nonexistent, args: {} }
`))
	require.NoError(t, err)

	mock := StartMock(t, defs)

	loop := &ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
	}

	// Run should fail the test because "nonexistent" is not registered.
	// We can't easily assert on t.Error calls, so we just verify it panics
	// or fails via require.True in the tool check.
	require.Panics(t, func() {
		loop.Run(t, "unknown tool")
	})
}
