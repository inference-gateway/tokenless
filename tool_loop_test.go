package tokenless

import (
	"context"
	"encoding/json"
	"os"
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

	cfg := "config.yaml"
	t.Cleanup(func() { os.Remove(cfg) })
	require.NoError(t, os.WriteFile(cfg, []byte("port: 8080\n"), 0o600))

	mock := StartMock(t, defs)

	loop := &ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
		Tools: map[string]ToolFunc{
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

	cfg := "config.yaml"
	t.Cleanup(func() { os.Remove(cfg) })
	require.NoError(t, os.WriteFile(cfg, []byte("port: 8080\n"), 0o600))

	mock := StartMock(t, defs)

	loop := &ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
	}
	loop.LoadScenarioTools(defs)

	loop.Tools["read_file"] = func(ctx context.Context, args json.RawMessage) (string, error) {
		var a struct {
			Path string `json:"path"`
		}
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
