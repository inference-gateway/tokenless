package tokenless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

// ToolFunc is a real tool implementation: given the tool call's JSON args,
// it returns the string result to feed back as a role:tool message.
type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// ToolLoopResult holds the final assistant content after all tool calls
// have been resolved.
type ToolLoopResult struct {
	FinalContent string
}

// ToolLoop drives a multi-turn conversation against a tokenless mock,
// executing real tool implementations when the mock returns tool_calls.
// Unregistered tool names cause a test failure.
type ToolLoop struct {
	BaseURL string
	Model   string
	Tools   map[string]ToolFunc

	// Context is the context passed to each ToolFunc invocation. If nil,
	// context.Background() is used. Set this to inject test helpers (e.g.
	// a *Mock for in-tool assertions) via context.WithValue.
	Context context.Context
}

// Run sends prompt as a user message and loops until the model responds
// with content (no more tool_calls). Each tool_calls turn invokes the
// matching ToolFunc and feeds the real result back as a role:tool message.
func (l *ToolLoop) Run(t testing.TB, prompt string) *ToolLoopResult {
	t.Helper()

	messages := []gateway.Message{
		{Role: gateway.User, Content: gateway.Text(prompt)},
	}

	client := &http.Client{}

	for {
		req := gateway.CreateChatCompletionRequest{
			Model:    l.Model,
			Messages: messages,
		}
		b, err := json.Marshal(req)
		require.NoError(t, err)

		resp, err := client.Post(l.BaseURL+"/v1/chat/completions", "application/json", bytes.NewReader(b))
		require.NoError(t, err)

		rb, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode, "unexpected status: body=%s", string(rb))

		var chatResp gateway.CreateChatCompletionResponse
		require.NoError(t, json.Unmarshal(rb, &chatResp))
		require.Len(t, chatResp.Choices, 1)

		choice := chatResp.Choices[0]
		messages = append(messages, choice.Message)

		if choice.FinishReason != gateway.ToolCalls {
			return &ToolLoopResult{FinalContent: choice.Message.Content.Text()}
		}

		require.NotNil(t, choice.Message.ToolCalls)
		for _, tc := range *choice.Message.ToolCalls {
			fn, ok := l.Tools[tc.Function.Name]
			require.True(t, ok, "unregistered tool: %q", tc.Function.Name)

			ctx := l.Context
			if ctx == nil {
				ctx = context.Background()
			}
			result, err := fn(ctx, json.RawMessage(tc.Function.Arguments))
			require.NoError(t, err, "tool %q failed", tc.Function.Name)

			id := tc.ID
			messages = append(messages, gateway.Message{
				Role:       gateway.Tool,
				ToolCallID: &id,
				Content:    gateway.Text(result),
			})
		}
	}
}

// LoadScenarioTools reads the tools: block from a ScenarioFile and
// registers each exec-based tool as a ToolFunc that templates the argv
// from the tool call's JSON args and runs the resulting command.
func (l *ToolLoop) LoadScenarioTools(defs *gateway.ScenarioFile) {
	if l.Tools == nil {
		l.Tools = make(map[string]ToolFunc, len(defs.Tools))
	}
	for name, def := range defs.Tools {
		name, def := name, def
		l.Tools[name] = func(ctx context.Context, args json.RawMessage) (string, error) {
			var data map[string]any
			if err := json.Unmarshal(args, &data); err != nil {
				return "", fmt.Errorf("tool %q: unmarshal args: %w", name, err)
			}
			cmd := make([]string, len(def.Exec))
			for i, arg := range def.Exec {
				tmpl, err := template.New("").Parse(arg)
				if err != nil {
					return "", fmt.Errorf("tool %q: template %q: %w", name, arg, err)
				}
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, data); err != nil {
					return "", fmt.Errorf("tool %q: execute %q: %w", name, arg, err)
				}
				cmd[i] = buf.String()
			}
			c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
			out, err := c.Output()
			if err != nil {
				return "", fmt.Errorf("tool %q: %v: %w", name, cmd, err)
			}
			return string(out), nil
		}
	}
}
