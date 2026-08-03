package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// messagesEvent decodes the Anthropic stream-event fields the tests assert on.
type messagesEvent struct {
	Type    string `json:"type"`
	Message struct {
		Usage struct {
			InputTokens              int64  `json:"input_tokens"`
			CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Delta struct {
		Type        *string `json:"type"`
		StopReason  *string `json:"stop_reason"`
		Thinking    *string `json:"thinking"`
		PartialJSON *string `json:"partial_json"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// messagesResponse decodes the sync Anthropic response fields the tests assert on.
type messagesResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens          int64  `json:"input_tokens"`
		OutputTokens         int64  `json:"output_tokens"`
		CacheReadInputTokens *int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// postMessages sends a /v1/messages request built from raw JSON so the test
// does not depend on the SDK's union builders.
func postMessages(t *testing.T, baseURL string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(baseURL+"/v1/messages?provider=anthropic", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	return resp
}

func messagesBody(prompt string, assistants int, stream bool) map[string]any {
	messages := []map[string]any{{"role": "user", "content": prompt}}
	for i := 0; i < assistants; i++ {
		messages = append(messages, map[string]any{"role": "assistant", "content": "ok"})
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "tool_result", "tool_use_id": "call_0_0", "content": "result"}},
		})
	}
	return map[string]any{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 4096,
		"stream":     stream,
		"messages":   messages,
	}
}

func readMessagesEvents(t *testing.T, body *http.Response) []messagesEvent {
	t.Helper()
	var events []messagesEvent
	scanner := bufio.NewScanner(body.Body)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var ev messagesEvent
		require.NoError(t, json.Unmarshal([]byte(payload), &ev), "non-JSON frame: %s", payload)
		events = append(events, ev)
	}
	require.NoError(t, scanner.Err())
	return events
}

// TestMessagesStream drives the anthropic-cache scenario's first turn over
// native SSE: thinking block, tool_use block with multi-fragment
// input_json_delta, Anthropic usage split (input excludes cache writes), and
// termination via message_stop with no [DONE] sentinel.
func TestMessagesStream(t *testing.T) {
	srv := New(Default())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postMessages(t, ts.URL, messagesBody("exercise the anthropic cache", 0, true))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "anthropic-cache", resp.Header.Get("X-Tokenless-Scenario"))

	events := readMessagesEvents(t, resp)
	types := make([]string, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}

	require.Equal(t, "message_start", types[0])
	u := events[0].Message.Usage
	require.Equal(t, int64(130), u.InputTokens, "input_tokens must exclude cache reads and writes")
	require.Equal(t, int64(100), *u.CacheCreationInputTokens)
	require.Equal(t, int64(0), *u.CacheReadInputTokens)

	require.Equal(t, "message_stop", types[len(types)-1])
	require.Equal(t, "message_delta", types[len(types)-2])
	require.Equal(t, "tool_use", *events[len(events)-2].Delta.StopReason)
	require.Equal(t, int64(25), events[len(events)-2].Usage.OutputTokens)

	var thinking, args strings.Builder
	jsonFragments := 0
	for _, ev := range events {
		if ev.Type != "content_block_delta" {
			continue
		}
		switch *ev.Delta.Type {
		case "thinking_delta":
			thinking.WriteString(*ev.Delta.Thinking)
		case "input_json_delta":
			args.WriteString(*ev.Delta.PartialJSON)
			jsonFragments++
		}
	}
	require.Equal(t, "Deciding which file to inspect first.", thinking.String())
	require.JSONEq(t, `{"file_path":"a.txt"}`, args.String())
	require.GreaterOrEqual(t, jsonFragments, 2, "argument accumulation must be exercised")

	recorded := srv.Requests()
	require.Len(t, recorded, 1)
	require.Equal(t, "/v1/messages", recorded[0].Endpoint)
	require.NotNil(t, recorded[0].MessagesBody)
	require.Equal(t, "anthropic", recorded[0].Provider)
}

// TestMessagesSync exercises the second turn (tool_result-only user messages
// must not re-anchor the scenario) via the sync JSON path.
func TestMessagesSync(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp := postMessages(t, ts.URL, messagesBody("exercise the anthropic cache", 1, false))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "1", resp.Header.Get("X-Tokenless-Step"))

	var msg messagesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&msg))
	require.Equal(t, "end_turn", msg.StopReason)
	require.Equal(t, int64(140), msg.Usage.InputTokens)
	require.Equal(t, int64(120), *msg.Usage.CacheReadInputTokens)
	require.Equal(t, int64(12), msg.Usage.OutputTokens)

	require.Len(t, msg.Content, 1)
	require.Equal(t, "text", msg.Content[0].Type)
	require.Equal(t, "Cache exercised.", msg.Content[0].Text)
}
