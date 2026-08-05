// Driving a multi-turn conversation: a scenario with a tool call turn followed
// by a content turn. The test sends the initial prompt, receives tool calls,
// sends the tool result back, and receives the final content — asserting on
// the turn counter (X-Tokenless-Step) at each stage.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
	gateway "github.com/inference-gateway/tokenless/gateway"
)

func TestMultiTurnConversation(t *testing.T) {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: write-approved
    match: '(?i)create a file named approved\.txt'
    turns:
      - tool_calls:
          - { name: Write, args: { file_path: "approved.txt", content: "hi" } }
      - content: "The file was written."
`))
	require.NoError(t, err)
	mock := tokenless.StartMock(t, defs)

	// Turn 0: send the prompt, expect a tool call.
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"create a file named approved.txt"}]}`
	resp, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "write-approved", resp.Header.Get("X-Tokenless-Scenario"))
	require.Equal(t, "0", resp.Header.Get("X-Tokenless-Step"))

	var turn0 gateway.CreateChatCompletionResponse
	b, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(b, &turn0))
	require.Len(t, turn0.Choices, 1)
	require.Equal(t, gateway.ToolCalls, turn0.Choices[0].FinishReason)
	require.NotNil(t, turn0.Choices[0].Message.ToolCalls)
	require.Len(t, *turn0.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "Write", (*turn0.Choices[0].Message.ToolCalls)[0].Function.Name)

	// Turn 1: send the tool result back, expect content.
	body2 := `{"model":"gpt-4o","messages":[{"role":"user","content":"create a file named approved.txt"},{"role":"assistant","tool_calls":[{"id":"call_0_0","type":"function","function":{"name":"Write","arguments":"{\"file_path\":\"approved.txt\",\"content\":\"hi\"}"}}]},{"role":"tool","tool_call_id":"call_0_0","content":"done"}]}`
	resp2, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body2))
	require.NoError(t, err)
	defer resp2.Body.Close()

	require.Equal(t, 200, resp2.StatusCode)
	require.Equal(t, "write-approved", resp2.Header.Get("X-Tokenless-Scenario"))
	require.Equal(t, "1", resp2.Header.Get("X-Tokenless-Step"))

	var turn1 gateway.CreateChatCompletionResponse
	b2, _ := io.ReadAll(resp2.Body)
	require.NoError(t, json.Unmarshal(b2, &turn1))
	require.Len(t, turn1.Choices, 1)
	require.Equal(t, gateway.Stop, turn1.Choices[0].FinishReason)
	require.Equal(t, "The file was written.", turn1.Choices[0].Message.Content.Text())

	// Turn 2: past the last turn, fallback is served.
	body3 := `{"model":"gpt-4o","messages":[{"role":"user","content":"create a file named approved.txt"},{"role":"assistant","tool_calls":[{"id":"call_0_0","type":"function","function":{"name":"Write","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_0_0","content":"done"},{"role":"assistant","content":"The file was written."},{"role":"user","content":"create a file named approved.txt"}]}`
	resp3, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body3))
	require.NoError(t, err)
	defer resp3.Body.Close()

	require.Equal(t, 200, resp3.StatusCode)
	require.Equal(t, "write-approved", resp3.Header.Get("X-Tokenless-Scenario"))
	require.Equal(t, "2", resp3.Header.Get("X-Tokenless-Step"))

	var turn2 gateway.CreateChatCompletionResponse
	b3, _ := io.ReadAll(resp3.Body)
	require.NoError(t, json.Unmarshal(b3, &turn2))
	require.Equal(t, "Done.", turn2.Choices[0].Message.Content.Text())
}
