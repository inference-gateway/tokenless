package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustText(t *testing.T, role MessageRole, text string) Message {
	t.Helper()
	return Message{Role: role, Content: Text(text)}
}

func chatRequest(t *testing.T, prompt string, assistants int, stream bool) *CreateChatCompletionRequest {
	t.Helper()
	msgs := []Message{
		mustText(t, System, "you are a test agent"),
		mustText(t, User, prompt),
	}
	for range assistants {
		msgs = append(msgs, mustText(t, Assistant, "prior answer"))
	}
	return &CreateChatCompletionRequest{Model: "gpt-4o", Messages: msgs, Stream: &stream}
}

func postCompletion(t *testing.T, baseURL string, req *CreateChatCompletionRequest) *http.Response {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	resp, err := http.Post(baseURL+"/v1/chat/completions?provider=openai", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	return resp
}

// readFrames parses an SSE body into decoded chunks, the raw data payloads,
// and whether the [DONE] sentinel arrived.
func readFrames(t *testing.T, body io.Reader) ([]CreateChatCompletionStreamResponse, []string, bool) {
	t.Helper()
	var frames []CreateChatCompletionStreamResponse
	var raw []string
	done := false

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		raw = append(raw, payload)
		if payload == "[DONE]" {
			done = true
			continue
		}
		var chunk CreateChatCompletionStreamResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
			frames = append(frames, chunk)
		}
	}
	require.NoError(t, scanner.Err())
	return frames, raw, done
}

func TestDefaultScenariosAreValid(t *testing.T) {
	defs := Default()
	require.Len(t, defs.Scenarios, 21)
	require.Equal(t, "Done.", defs.Fallback.Content)
}

func TestResolve(t *testing.T) {
	defs := Default()

	tests := []struct {
		name         string
		prompt       string
		assistants   int
		wantScenario string
		wantContent  string
	}{
		{"first matching scenario wins", "say hello to everyone", 0, "text-only", "Hello! How can I help?"},
		{"case insensitive", "PLEASE SEARCH FOR todo items", 0, "search", ""},
		{"step selects later turn", "explore the project structure", 2, "sequential-explore", "The project contains a.txt with fixture content."},
		{"step past end falls back", "say hello", 1, "text-only", "Done."},
		{"no match falls back", "completely unknown prompt", 0, "", "Done."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, step, turn := defs.resolve(chatRequest(t, tt.prompt, tt.assistants, false))
			require.Equal(t, tt.wantScenario, name)
			require.Equal(t, tt.assistants, step)
			require.Equal(t, tt.wantContent, turn.Content)
		})
	}
}

func TestResolveModelFilter(t *testing.T) {
	defs := Default()

	t.Run("model filter skips when model does not match", func(t *testing.T) {
		// The model-specific scenario requires "gpt-4o-mini", but the request
		// uses "gpt-4o" — it should be skipped and fall through to fallback.
		stream := false
		req := &CreateChatCompletionRequest{Model: "gpt-4o", Messages: []Message{
			mustText(t, System, "you are a test agent"),
			mustText(t, User, "model specific"),
		}, Stream: &stream}
		name, step, turn := defs.resolve(req)
		require.Equal(t, "", name, "model-specific scenario should be skipped when model doesn't match")
		require.Equal(t, 0, step)
		require.Equal(t, "Done.", turn.Content)
	})

	t.Run("model filter matches when model matches", func(t *testing.T) {
		stream := false
		req := &CreateChatCompletionRequest{Model: "gpt-4o-mini", Messages: []Message{
			mustText(t, System, "you are a test agent"),
			mustText(t, User, "model specific"),
		}, Stream: &stream}
		name, step, turn := defs.resolve(req)
		require.Equal(t, "model-specific", name)
		require.Equal(t, 0, step)
		require.Equal(t, "This is a model-specific response for gpt-4o-mini.", turn.Content)
	})

	t.Run("model filter does not affect scenarios without model set", func(t *testing.T) {
		// text-only has no model constraint, so it should match regardless
		// of the request model.
		stream := false
		req := &CreateChatCompletionRequest{Model: "gpt-4o-mini", Messages: []Message{
			mustText(t, System, "you are a test agent"),
			mustText(t, User, "say hello"),
		}, Stream: &stream}
		name, step, turn := defs.resolve(req)
		require.Equal(t, "text-only", name)
		require.Equal(t, 0, step)
		require.Equal(t, "Hello! How can I help?", turn.Content)
	})
}

func TestResolveIgnoresLaterUserMessages(t *testing.T) {
	defs := Default()
	req := chatRequest(t, "say hello", 1, false)
	req.Messages = append(req.Messages, mustText(t, User, "<system-reminder>automated check</system-reminder>"))

	name, step, _ := defs.resolve(req)
	require.Equal(t, "text-only", name)
	require.Equal(t, 1, step)
}

func TestResolveIgnoresJobCompletionNotices(t *testing.T) {
	defs := Default()
	req := chatRequest(t, "run a background shell", 1, false)
	req.Messages = append(req.Messages, mustText(t, User, "[Background Shell Completed: shell-123]\n\nExit code: 0"))

	name, step, turn := defs.resolve(req)
	require.Equal(t, "background-shell", name)
	require.Equal(t, 1, step)
	require.Equal(t, "The background shell task ran.", turn.Content)
}

func TestResolveReroutesOnNewChatPrompt(t *testing.T) {
	defs := Default()
	req := chatRequest(t, "Hi", 1, false)
	req.Messages = append(req.Messages, mustText(t, User, "Say hello"))

	name, step, turn := defs.resolve(req)
	require.Equal(t, "text-only", name)
	require.Equal(t, 0, step)
	require.Equal(t, "Hello! How can I help?", turn.Content)
}

func TestResolveConcatenatesContentParts(t *testing.T) {
	defs := Default()

	var content MessageContent
	require.NoError(t, json.Unmarshal([]byte(`[
		{"type": "text", "text": "say hello"},
		{"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}}
	]`), &content))

	stream := false
	req := &CreateChatCompletionRequest{Model: "gpt-4o", Messages: []Message{{Role: User, Content: content}}, Stream: &stream}
	name, _, _ := defs.resolve(req)
	require.Equal(t, "text-only", name)
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"unknown field", "scenarios:\n  - name: a\n    match: x\n    turns: [{content: hi}]\n    typo: true\n", "field typo not found"},
		{"missing name", "scenarios:\n  - match: x\n    turns: [{content: hi}]\n", "name is required"},
		{"duplicate name", "scenarios:\n  - {name: a, match: x, turns: [{content: hi}]}\n  - {name: a, match: y, turns: [{content: hi}]}\n", "duplicate name"},
		{"bad regex", "scenarios:\n  - {name: a, match: '([', turns: [{content: hi}]}\n", "invalid match"},
		{"no turns", "scenarios:\n  - {name: a, match: x, turns: []}\n", "at least one turn"},
		{"retryable 400 rejected", "scenarios:\n  - {name: a, match: x, turns: [{error: {status: 400, times: 1}}]}\n", "error.status"},
		{"zero times rejected", "scenarios:\n  - {name: a, match: x, turns: [{error: {status: 500, times: 0}}]}\n", "error.times"},
		{"tool call needs name", "scenarios:\n  - {name: a, match: x, turns: [{tool_calls: [{args: {k: v}}]}]}\n", "name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load([]byte(tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestModelsAndHealthEndpoints(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var models ListModelsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&models))
	require.Len(t, models.Data, 4)
	require.Equal(t, DefaultModel, models.Data[0].ID)
	require.Equal(t, AnthropicModel, models.Data[1].ID)
	require.Equal(t, ImageModel, models.Data[2].ID)
	require.Equal(t, DeepseekModel, models.Data[3].ID)

	health, err := http.Get(ts.URL + "/v1/health")
	require.NoError(t, err)
	defer func() { _ = health.Body.Close() }()
	require.Equal(t, http.StatusOK, health.StatusCode)

	unknown, err := http.Get(ts.URL + "/v1/nope")
	require.NoError(t, err)
	defer func() { _ = unknown.Body.Close() }()
	require.Equal(t, http.StatusNotFound, unknown.StatusCode)
}

func TestSyncTextResponse(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp := postCompletion(t, ts.URL, chatRequest(t, "say hello", 0, false))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out CreateChatCompletionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out.Choices, 1)
	require.Equal(t, Stop, out.Choices[0].FinishReason)

	require.Equal(t, "Hello! How can I help?", out.Choices[0].Message.Content.Text())
	require.NotNil(t, out.Usage)
	require.EqualValues(t, 15, out.Usage.TotalTokens)
}

func TestSyncToolCalls(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp := postCompletion(t, ts.URL, chatRequest(t, "please execute the Read tool 4 times in parallel", 0, false))
	defer func() { _ = resp.Body.Close() }()

	var out CreateChatCompletionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, ToolCalls, out.Choices[0].FinishReason)
	require.NotNil(t, out.Choices[0].Message.ToolCalls)

	calls := *out.Choices[0].Message.ToolCalls
	require.Len(t, calls, 4)
	for i, call := range calls {
		require.Equal(t, "Read", call.Function.Name)
		require.NotEmpty(t, call.ID)
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(call.Function.Arguments), &args), "call %d arguments must be valid JSON", i)
		require.Contains(t, args, "file_path")
	}
}

func TestStreamTextAndReasoningRoundTrip(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp := postCompletion(t, ts.URL, chatRequest(t, "think step by step", 0, true))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	frames, _, done := readFrames(t, resp.Body)
	require.True(t, done, "stream must terminate with [DONE]")

	var content, reasoning strings.Builder
	var usage *CompletionUsage
	for _, f := range frames {
		if f.Usage != nil {
			usage = f.Usage
			require.Empty(t, f.Choices, "usage chunk must carry no choices")
		}
		for _, c := range f.Choices {
			content.WriteString(c.Delta.Content)
			if c.Delta.ReasoningContent != nil {
				reasoning.WriteString(*c.Delta.ReasoningContent)
			}
		}
	}

	require.Equal(t, "Answer: 42.", content.String())
	require.Equal(t, "Considering the question carefully before answering.", reasoning.String())
	require.NotNil(t, usage)
	require.EqualValues(t, 15, usage.TotalTokens)
}

func TestStreamToolCallFragments(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp := postCompletion(t, ts.URL, chatRequest(t, "please execute the Read tool 4 times in parallel", 0, true))
	defer func() { _ = resp.Body.Close() }()

	frames, _, done := readFrames(t, resp.Body)
	require.True(t, done)

	type acc struct {
		id, name, args string
		argFragments   int
	}
	byIndex := map[int]*acc{}
	for _, f := range frames {
		for _, c := range f.Choices {
			if c.Delta.ToolCalls == nil {
				continue
			}
			for _, tc := range *c.Delta.ToolCalls {
				a := byIndex[tc.Index]
				if a == nil {
					a = &acc{}
					byIndex[tc.Index] = a
				}
				if tc.ID != nil {
					a.id = *tc.ID
				}
				if tc.Function == nil {
					continue
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					a.args += tc.Function.Arguments
					a.argFragments++
				}
			}
		}
	}

	require.Len(t, byIndex, 4)
	for i := range 4 {
		a := byIndex[i]
		require.NotNil(t, a, "missing tool call index %d", i)
		require.Equal(t, "Read", a.name)
		require.NotEmpty(t, a.id)
		require.GreaterOrEqual(t, a.argFragments, 2, "arguments for index %d must arrive fragmented", i)
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(a.args), &args), "reassembled arguments for index %d", i)
		require.Contains(t, args, "file_path")
	}
}

func TestStreamMalformedFrameThenRecovers(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	resp := postCompletion(t, ts.URL, chatRequest(t, "give me a garbled stream", 0, true))
	defer func() { _ = resp.Body.Close() }()

	frames, raw, done := readFrames(t, resp.Body)
	require.True(t, done)
	require.Contains(t, raw, "{this is not json")

	var content strings.Builder
	for _, f := range frames {
		for _, c := range f.Choices {
			content.WriteString(c.Delta.Content)
		}
	}
	require.Equal(t, "Still standing.", content.String())
}

func TestErrorInjectionCountsThenRecovers(t *testing.T) {
	srv := New(Default())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	for _, wantStatus := range []int{429, 429, 200} {
		resp := postCompletion(t, ts.URL, chatRequest(t, "you are a flaky backend", 0, false))
		require.Equal(t, wantStatus, resp.StatusCode)
		_ = resp.Body.Close()
	}

	require.Len(t, srv.Requests(), 3)
}

func TestErrorInjectionForever(t *testing.T) {
	ts := httptest.NewServer(New(Default()))
	defer ts.Close()

	for range 4 {
		resp := postCompletion(t, ts.URL, chatRequest(t, "this always fails", 0, false))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		_ = resp.Body.Close()
	}
}

func TestRequestsRecording(t *testing.T) {
	srv := New(Default())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postCompletion(t, ts.URL, chatRequest(t, "say hello", 1, true))
	_ = resp.Body.Close()

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	require.Equal(t, "openai", reqs[0].Provider)
	require.Equal(t, "gpt-4o", reqs[0].Model)
	require.Equal(t, "text-only", reqs[0].Scenario)
	require.Equal(t, 1, reqs[0].Step)
	require.True(t, reqs[0].Stream)
	require.Len(t, reqs[0].Body.Messages, 3)
}

func TestImagesEndpoints(t *testing.T) {
	srv := New(Default())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
		body := `{"prompt": "a red square", "model": "gpt-image-2", "size": "1024x1024", "quality": "high"}`
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		require.NoError(t, err)
		var out ImagesResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		_ = resp.Body.Close()
		require.Len(t, out.Data, 1, path)
		require.NotEmpty(t, *out.Data[0].B64Json, path)
	}

	reqs := srv.Requests()
	require.Len(t, reqs, 2)
	require.Equal(t, "/v1/images/edits", reqs[1].Endpoint)
	require.Equal(t, "a red square", reqs[1].ImagesBody.Prompt)
	require.Equal(t, "high", *reqs[1].ImagesBody.Quality)
}
