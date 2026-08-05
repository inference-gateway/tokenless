// Testing tool call approval: the ToolLoop's Approve callback lets tests
// approve or reject each tool call before execution. Approved tools run
// normally; rejected tools return a "denied" result instead, and the loop
// continues with the next turn.
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

// TestApproveTool demonstrates approving a specific tool call. The mock
// returns a read_file tool call; the ApprovalFunc approves it, the tool
// executes, and the final content reflects the file content.
func TestApproveTool(t *testing.T) {
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

	approved := make([]string, 0)
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
		// Approve: only allow read_file.
		Approve: func(ctx context.Context, tc gateway.ChatCompletionMessageToolCall) bool {
			approved = append(approved, tc.Function.Name)
			return tc.Function.Name == "read_file"
		},
	}

	result := loop.Run(t, "summarize config.yaml")
	require.Contains(t, result.FinalContent, "port 8080")
	require.Equal(t, []string{"read_file"}, approved)
	mock.AssertExpectations(t)
}

// TestRejectTool demonstrates rejecting a tool call. The ApprovalFunc
// rejects write_file, so the tool is never executed and the loop
// continues with the next turn.
func TestRejectTool(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: write-file
    match: '(?i)write.*file'
    turns:
      - tool_calls:
          - { name: write_file, args: { file_path: "secret.txt", content: "data" } }
      - content: "The file was written."
`))
	require.NoError(t, err)

	mock := tokenless.StartMock(t, defs)

	loop := &tokenless.ToolLoop{
		BaseURL: mock.URL,
		Model:   "gpt-4o",
		Tools: map[string]tokenless.ToolFunc{
			"write_file": func(ctx context.Context, args json.RawMessage) (string, error) {
				return "", nil
			},
		},
		// Approve: reject write_file.
		Approve: func(ctx context.Context, tc gateway.ChatCompletionMessageToolCall) bool {
			return false
		},
	}

	result := loop.Run(t, "write a file")
	// The tool was rejected, so the loop continues and the
	// scenario's next turn provides the response.
	require.Contains(t, result.FinalContent, "The file was written.")
	mock.AssertExpectations(t)
}

// TestApproveAll demonstrates that a nil Approve (default) approves all
// tool calls, preserving backward compatibility.
func TestApproveAll(t *testing.T) {
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

	// No Approve set — all tools approved by default.
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
