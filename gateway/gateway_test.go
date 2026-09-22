package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		{"videos negative polls", "videos: {polls_until_complete: -1}\n" + "scenarios:\n  - {name: a, match: x, turns: [{content: hi}]}\n", "polls_until_complete"},
		{"videos bad error status", "videos: {error: {status: 400, times: 1}}\n" + "scenarios:\n  - {name: a, match: x, turns: [{content: hi}]}\n", "videos: error.status"},
		{"videos zero error times", "videos: {error: {status: 503, times: 0}}\n" + "scenarios:\n  - {name: a, match: x, turns: [{content: hi}]}\n", "videos: error.times"},
		{"videos bad stall times", "videos: {stall: {times: 0}}\n" + "scenarios:\n  - {name: a, match: x, turns: [{content: hi}]}\n", "videos: stall.times"},
		{"videos fail incomplete", "videos: {fail: {code: x}}\n" + "scenarios:\n  - {name: a, match: x, turns: [{content: hi}]}\n", "fail.code"},
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

func TestCustomModels(t *testing.T) {
	defs, err := Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: test
    match: x
    turns:
      - content: "y"
models:
  - id: custom-model
    object: model
    owned_by: test
    served_by: test
`))
	require.NoError(t, err)
	require.Len(t, defs.Models, 1)
	require.Equal(t, "custom-model", defs.Models[0].ID)

	ts := httptest.NewServer(New(defs))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var models ListModelsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&models))
	require.Len(t, models.Data, 1)
	require.Equal(t, "custom-model", models.Data[0].ID)
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

func TestMusicEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCT     string
		wantLen    int
		wantFormat string
	}{
		{"honors duration_seconds", `{"model": "music_v2_5", "prompt": "lo-fi beats", "duration_seconds": 2.5, "instrumental": true, "response_format": "mp3"}`, http.StatusOK, "audio/mpeg", 96 * 417, "mp3"},
		{"defaults to one second", `{"model": "music_v2_5", "prompt": "lo-fi beats"}`, http.StatusOK, "audio/mpeg", 39 * 417, ""},
		{"non-positive duration defaults", `{"model": "music_v2_5", "prompt": "lo-fi beats", "duration_seconds": -3}`, http.StatusOK, "audio/mpeg", 39 * 417, ""},
		{"wav is rejected", `{"model": "music_v2_5", "prompt": "lo-fi beats", "response_format": "wav"}`, http.StatusBadRequest, "", 0, "wav"},
		{"pcm is headerless", `{"model": "music_v2_5", "prompt": "lo-fi beats", "response_format": "pcm"}`, http.StatusOK, "audio/pcm", 88200, "pcm"},
		{"opus is rejected", `{"model": "music_v2_5", "prompt": "lo-fi beats", "response_format": "opus"}`, http.StatusBadRequest, "", 0, "opus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Default())
			ts := httptest.NewServer(srv)
			defer ts.Close()

			resp, err := http.Post(ts.URL+"/v1/audio/music", "application/json", strings.NewReader(tt.body))
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.wantStatus == http.StatusOK {
				require.Equal(t, tt.wantCT, resp.Header.Get("Content-Type"))
				switch tt.wantCT {
				case "audio/mpeg":
					require.Len(t, body, tt.wantLen)
					for start := 0; start < len(body); start += 417 {
						frame := body[start : start+417]
						require.Equal(t, []byte{0xFF, 0xFB, 0x90, 0xC0}, frame[:4])
						require.Equal(t, make([]byte, 413), frame[4:])
					}
				case "audio/pcm":
					require.Len(t, body, tt.wantLen)
					require.Equal(t, make([]byte, tt.wantLen), body)
				}
			} else {
				var e struct {
					Error string `json:"error"`
				}
				require.NoError(t, json.Unmarshal(body, &e))
				require.Contains(t, e.Error, `response_format "`+tt.wantFormat+`"`)
				require.Contains(t, e.Error, "supported formats: mp3, pcm")
			}

			reqs := srv.Requests()
			require.Len(t, reqs, 1)
			require.Equal(t, "/v1/audio/music", reqs[0].Endpoint)
			require.Equal(t, "music_v2_5", reqs[0].Model)
			require.Equal(t, "lo-fi beats", reqs[0].MusicBody.Prompt)
			if reqs[0].MusicBody.ResponseFormat != nil {
				require.Equal(t, tt.wantFormat, *reqs[0].MusicBody.ResponseFormat)
			}
		})
	}
}

func TestSFXEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCT     string
		wantLen    int
		wantFormat string
	}{
		{"honors duration_seconds", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh", "duration_seconds": 2.5, "response_format": "mp3"}`, http.StatusOK, "audio/mpeg", 96 * 417, "mp3"},
		{"defaults to one second", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh"}`, http.StatusOK, "audio/mpeg", 39 * 417, ""},
		{"non-positive duration defaults", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh", "duration_seconds": -3}`, http.StatusOK, "audio/mpeg", 39 * 417, ""},
		{"unset defaults to mp3", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh"}`, http.StatusOK, "audio/mpeg", 39 * 417, ""},
		{"mp3", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh", "response_format": "mp3"}`, http.StatusOK, "audio/mpeg", 39 * 417, "mp3"},
		{"pcm is headerless", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh", "response_format": "pcm"}`, http.StatusOK, "audio/pcm", 88200, "pcm"},
		{"wav is rejected", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh", "response_format": "wav"}`, http.StatusBadRequest, "", 0, "wav"},
		{"opus is rejected", `{"model": "elevenlabs/eleven_text_to_sound_v2", "prompt": "a fast whoosh", "response_format": "opus"}`, http.StatusBadRequest, "", 0, "opus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Default())
			ts := httptest.NewServer(srv)
			defer ts.Close()

			resp, err := http.Post(ts.URL+"/v1/audio/sfx", "application/json", strings.NewReader(tt.body))
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.wantStatus == http.StatusOK {
				require.Equal(t, tt.wantCT, resp.Header.Get("Content-Type"))
				switch tt.wantCT {
				case "audio/mpeg":
					require.Len(t, body, tt.wantLen)
					for start := 0; start < len(body); start += 417 {
						frame := body[start : start+417]
						require.Equal(t, []byte{0xFF, 0xFB, 0x90, 0xC0}, frame[:4])
						require.Equal(t, make([]byte, 413), frame[4:])
					}
				case "audio/pcm":
					require.Len(t, body, tt.wantLen)
					require.Equal(t, make([]byte, tt.wantLen), body)
				}
			} else {
				var e struct {
					Error string `json:"error"`
				}
				require.NoError(t, json.Unmarshal(body, &e))
				require.Contains(t, e.Error, `response_format "`+tt.wantFormat+`"`)
				require.Contains(t, e.Error, "supported formats: mp3, pcm")
			}

			reqs := srv.Requests()
			require.Len(t, reqs, 1)
			require.Equal(t, "/v1/audio/sfx", reqs[0].Endpoint)
			require.Equal(t, "elevenlabs/eleven_text_to_sound_v2", reqs[0].Model)
			require.Equal(t, "a fast whoosh", reqs[0].SFXBody.Prompt)
			if reqs[0].SFXBody.ResponseFormat != nil {
				require.Equal(t, tt.wantFormat, *reqs[0].SFXBody.ResponseFormat)
			}
		})
	}
}

// postVideoForm posts a multipart /v1/videos form with one model and one
// prompt field.
func postVideoForm(t *testing.T, baseURL, model, prompt string) *http.Response {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("model", model))
	require.NoError(t, w.WriteField("prompt", prompt))
	require.NoError(t, w.Close())
	resp, err := http.Post(baseURL+"/v1/videos", w.FormDataContentType(), body)
	require.NoError(t, err)
	return resp
}

// decodeVideoJob decodes and closes a VideoJob response.
func decodeVideoJob(t *testing.T, resp *http.Response) VideoJob {
	t.Helper()
	var job VideoJob
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&job))
	require.NoError(t, resp.Body.Close())
	return job
}

// videoDefs builds a scenario file with an optional top-level videos block.
func videoDefs(t *testing.T, videos string) *ScenarioFile {
	t.Helper()
	defs, err := Load([]byte(`fallback:
  content: "Done."
scenarios:
  - name: anything
    match: hi
    turns:
      - content: "Hello."
` + videos))
	require.NoError(t, err)
	return defs
}

func TestVideoJobLifecycle(t *testing.T) {
	srv := New(Default())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// POST /v1/videos records the multipart form and returns a queued job.
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("model", "sora-2"))
	require.NoError(t, w.WriteField("prompt", "a cat playing piano"))
	require.NoError(t, w.WriteField("seconds", "4"))
	require.NoError(t, w.WriteField("size", "720x720"))
	for _, name := range []string{"input_reference", "audio"} {
		fw, err := w.CreateFormFile(name, name+".bin")
		require.NoError(t, err)
		_, err = fw.Write([]byte("frame bytes"))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	resp, err := http.Post(ts.URL+"/v1/videos", w.FormDataContentType(), body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	job := decodeVideoJob(t, resp)
	require.Equal(t, "video", job.Object)
	require.Equal(t, "video-job-1", job.ID)
	require.Equal(t, VideoQueued, job.Status)
	require.Equal(t, "sora-2", job.Model)
	require.NotZero(t, job.CreatedAt)
	require.Nil(t, job.CompletedAt)
	require.Equal(t, "4", *job.Seconds)
	require.Equal(t, "720x720", *job.Size)

	// The creation is recorded, including the multipart field names.
	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	require.Equal(t, "/v1/videos", reqs[0].Endpoint)
	require.Equal(t, "sora-2", reqs[0].Model)
	require.Equal(t, "a cat playing piano", reqs[0].VideoBody.Prompt)
	require.ElementsMatch(t,
		[]string{"model", "prompt", "seconds", "size", "input_reference", "audio"},
		reqs[0].VideoBody.Fields)

	// Content is 404 until the job completes, and unknown ids 404.
	contentURL := ts.URL + "/v1/videos/" + job.ID + "/content"
	for _, path := range []string{"/v1/videos/" + job.ID + "/content", "/v1/videos/nope", "/v1/videos/nope/content"} {
		resp, err = http.Get(ts.URL + path)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}

	// Each poll advances the job one step.
	pollURL := ts.URL + "/v1/videos/" + job.ID
	resp, err = http.Get(pollURL)
	require.NoError(t, err)
	inProgress := decodeVideoJob(t, resp)
	require.Equal(t, VideoInProgress, inProgress.Status)
	require.Equal(t, 50, inProgress.Progress)
	require.Nil(t, inProgress.CompletedAt)

	resp, err = http.Get(contentURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	resp, err = http.Get(pollURL)
	require.NoError(t, err)
	completed := decodeVideoJob(t, resp)
	require.Equal(t, VideoCompleted, completed.Status)
	require.Equal(t, 100, completed.Progress)
	require.NotNil(t, completed.CompletedAt)

	// Once completed the content endpoint serves the canned MP4.
	resp, err = http.Get(contentURL)
	require.NoError(t, err)
	clip, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "video/mp4", resp.Header.Get("Content-Type"))
	require.Equal(t, videoClip, clip)
	require.Equal(t, "ftyp", string(clip[4:8]))

	// The top-level MP4 boxes must tile the file exactly.
	off := 0
	for off < len(clip) {
		size := int(binary.BigEndian.Uint32(clip[off:]))
		if size <= 0 {
			t.Fatalf("non-positive box size %d at offset %d", size, off)
		}
		off += size
	}
	require.Equal(t, len(clip), off)

	// Poll and content requests are recorded, so tests can assert polling.
	require.Len(t, srv.Requests(), 8)

	// A second creation gets a unique id.
	resp2 := postVideoForm(t, ts.URL, "sora-2", "another cat")
	require.Equal(t, "video-job-2", decodeVideoJob(t, resp2).ID)
}

func TestVideoJobFailurePath(t *testing.T) {
	defs := videoDefs(t, `videos:
  polls_until_complete: 1
  fail:
    code: provider_error
    message: "content policy"
`)
	srv := New(defs)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postVideoForm(t, ts.URL, "sora-2", "a dangerous prompt")
	job := decodeVideoJob(t, resp)
	require.Equal(t, VideoQueued, job.Status)

	// The first poll fails the job with the configured error payload.
	resp, err := http.Get(ts.URL + "/v1/videos/" + job.ID)
	require.NoError(t, err)
	failed := decodeVideoJob(t, resp)
	require.Equal(t, VideoFailed, failed.Status)
	require.NotNil(t, failed.Error)
	require.Equal(t, "provider_error", failed.Error.Code)
	require.Equal(t, "content policy", failed.Error.Message)
	require.NotNil(t, failed.CompletedAt)

	// A failed job never serves content.
	resp, err = http.Get(ts.URL + "/v1/videos/" + job.ID + "/content")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestVideoPollErrorInjection(t *testing.T) {
	defs := videoDefs(t, "videos:\n  error:\n    status: 503\n    times: 1\n")
	srv := New(defs)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	job := decodeVideoJob(t, postVideoForm(t, ts.URL, "sora-2", "retry me"))

	// The first poll gets the injected 503 and consumes no lifecycle step.
	resp, err := http.Get(ts.URL + "/v1/videos/" + job.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// A retrying client then walks the job through the normal lifecycle.
	resp, err = http.Get(ts.URL + "/v1/videos/" + job.ID)
	require.NoError(t, err)
	require.Equal(t, VideoInProgress, decodeVideoJob(t, resp).Status)
	resp, err = http.Get(ts.URL + "/v1/videos/" + job.ID)
	require.NoError(t, err)
	require.Equal(t, VideoCompleted, decodeVideoJob(t, resp).Status)

	// The injected poll is recorded like any other.
	require.Len(t, srv.Requests(), 4)
	for i := 1; i < 4; i++ {
		require.Equal(t, "/v1/videos/"+job.ID, srv.Requests()[i].Endpoint)
	}
}

func TestVideoPollStall(t *testing.T) {
	defs := videoDefs(t, "videos:\n  stall:\n    times: 1\n")
	srv := New(defs)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	job := decodeVideoJob(t, postVideoForm(t, ts.URL, "sora-2", "hang on me"))

	// The first poll hangs until the client times out.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/videos/"+job.ID, nil)
	require.NoError(t, err)
	_, err = http.DefaultClient.Do(req)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The stalled poll consumed no step; the next one advances normally.
	resp, err := http.Get(ts.URL + "/v1/videos/" + job.ID)
	require.NoError(t, err)
	require.Equal(t, VideoInProgress, decodeVideoJob(t, resp).Status)
}
