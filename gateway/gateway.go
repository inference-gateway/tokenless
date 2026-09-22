// Package gateway implements a hermetic, deterministic mock of the LLM
// API surface agent apps consume: GET /v1/models, POST /v1/chat/completions
// (sync JSON and SSE streaming), POST /v1/messages (Anthropic-native),
// POST /v1/images/generations and /v1/images/edits, POST /v1/audio/music,
// POST /v1/audio/sfx, the Videos API job lifecycle (POST /v1/videos,
// GET /v1/videos/{id}, GET /v1/videos/{id}/content), and GET /v1/health.
//
// Scenario resolution is stateless: every request carries the full message
// history, so the scenario is chosen by matching each scenario's regex
// against the first user message, and the turn within the scenario is the
// count of assistant messages already present in the request. The only
// mutable state is the error-injection counter and the request recording,
// both guarded by one mutex, which makes the server safe for concurrent use.
package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultModel is the primary model the mock advertises on /v1/models. Model
// ids carry the provider prefix; request bodies arrive with it stripped.
const DefaultModel = "mock/openai/gpt-4o"

// AnthropicModel is the Anthropic model the mock advertises; apps typically
// route it through POST /v1/messages (native Anthropic SSE) instead of
// /v1/chat/completions.
const AnthropicModel = "mock/anthropic/claude-sonnet-4-5"

// ImageModel is the image-generation model the mock advertises, for agents
// whose image tools post to /v1/images/generations or /v1/images/edits.
const ImageModel = "mock/openai/gpt-image-2"

// DeepseekModel is a text-only model for testing text-only modality filtering.
const DeepseekModel = "mock/deepseek/deepseek-v4-flash"

// Metadata advertised for every model on /v1/models: the gateway extensions
// (context_window, pricing) are always populated so cost accounting in the
// app under test sees real-looking numbers.
const (
	DefaultContextWindow   = 128000
	DefaultInputPrice      = "0.0000025"   // per token → $2.50 per MTok
	DefaultOutputPrice     = "0.00001"     // per token → $10.00 per MTok
	DefaultCachePrice      = "0.00000025"  // per token → $0.25 per MTok
	DefaultCacheWritePrice = "0.000003125" // per token → $3.125 per MTok (1.25x input)
)

const defaultChunkSize = 16

// Recorded captures one LLM request for test assertions.
type Recorded struct {
	// Endpoint is the request path (/v1/chat/completions or /v1/messages).
	Endpoint string
	// Provider is the ?provider= query parameter sent by the SDK.
	Provider string
	// Model is the request-body model (provider prefix already stripped).
	Model string
	// Scenario is the matched scenario name; empty when only the fallback applied.
	Scenario string
	// Step is the assistant-message count at the time of the request.
	Step int
	// Stream reports whether the request asked for SSE.
	Stream bool
	// Body is the full decoded request for /v1/chat/completions.
	Body CreateChatCompletionRequest
	// MessagesBody is the full decoded request for /v1/messages.
	MessagesBody *CreateMessagesRequest
	// ImagesBody is the full decoded request for /v1/images/generations.
	ImagesBody *CreateImageRequest
	// MusicBody is the full decoded request for /v1/audio/music.
	MusicBody *CreateMusicRequest
	// SFXBody is the full decoded request for /v1/audio/sfx.
	SFXBody *CreateSFXRequest
	// VideoBody is the full decoded multipart form for POST /v1/videos.
	VideoBody *CreateVideoRequest
	// RawBody is the request body verbatim, so apps can decode it with their
	// own richer types when the minimal ones above are not enough.
	RawBody json.RawMessage
}

// Server is an http.Handler implementing the mocked LLM API surface.
type Server struct {
	Model string

	defs *ScenarioFile

	mu     sync.Mutex
	fails  map[string]int
	reqs   []Recorded
	jobs   map[string]*videoJobState
	videoN int

	expectFails []ExpectFailure
}

// New returns a Server serving the given scenario definitions; with no
// arguments it serves the built-in library.
func New(defs ...*ScenarioFile) *Server {
	d := Default()
	if len(defs) > 0 && defs[0] != nil {
		d = defs[0]
	}
	return &Server{defs: d, fails: make(map[string]int), jobs: make(map[string]*videoJobState)}
}

// Requests returns a copy of all recorded chat-completion requests.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// ExpectFailures returns a copy of all recorded expectation failures.
func (s *Server) ExpectFailures() []ExpectFailure {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.expectFails)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		s.handleCompletions(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		s.handleMessages(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		if len(s.defs.Models) > 0 {
			writeJSON(w, ListModelsResponse{Object: "list", Data: s.defs.Models})
			break
		}
		cachePrice := DefaultCachePrice
		cacheWritePrice := DefaultCacheWritePrice
		pricing := Pricing{
			InputPerToken:      DefaultInputPrice,
			OutputPerToken:     DefaultOutputPrice,
			CacheReadPerToken:  &cachePrice,
			CacheWritePerToken: &cacheWritePrice,
			Currency:           "USD",
			Source:             "provider",
		}
		contextWindow := ContextWindow{Tokens: DefaultContextWindow, Source: "provider"}
		model := s.Model
		if model == "" {
			model = DefaultModel
		}
		writeJSON(w, ListModelsResponse{
			Object: "list",
			Data: []Model{
				{
					ID: model, Object: "model", OwnedBy: "openai", ServedBy: "openai",
					ContextWindow: &contextWindow, Pricing: &pricing,
					Modalities: &ModelModalities{Input: []Modality{"text", "image"}, Output: []Modality{"text"}},
				},
				{
					ID: AnthropicModel, Object: "model", OwnedBy: "anthropic", ServedBy: "anthropic",
					ContextWindow: &contextWindow, Pricing: &pricing,
					Modalities: &ModelModalities{Input: []Modality{"text", "image"}, Output: []Modality{"text"}},
				},
				{
					ID: ImageModel, Object: "model", OwnedBy: "openai", ServedBy: "openai",
					ContextWindow: &contextWindow, Pricing: &pricing,
					Modalities: &ModelModalities{Input: []Modality{"text", "image"}, Output: []Modality{"image"}},
				},
				{
					ID: DeepseekModel, Object: "model", OwnedBy: "deepseek", ServedBy: "deepseek",
					ContextWindow: &contextWindow, Pricing: &pricing,
					Modalities: &ModelModalities{Input: []Modality{"text"}, Output: []Modality{"text"}},
				},
			},
		})
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/images/generations" || r.URL.Path == "/v1/images/edits"):
		s.handleImages(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/music":
		s.handleMusic(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/sfx":
		s.handleSFX(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
		s.handleVideos(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/videos/"):
		s.handleVideoJob(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/expect":
		s.handleExpect(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/health":
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, fmt.Sprintf("tokenless: unexpected %s %s", r.Method, r.URL.Path), http.StatusNotFound)
	}
}

func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"reading request body"}`, http.StatusBadRequest)
		return
	}
	var req CreateChatCompletionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	name, step, turn := s.defs.resolve(&req)
	stream := req.Stream != nil && *req.Stream
	w.Header().Set("X-Tokenless-Scenario", name)
	w.Header().Set("X-Tokenless-Step", fmt.Sprintf("%d", step))
	if turn.Expect != nil {
		if fails := s.checkExpect(name, step, r.URL.Path, &req, turn.Expect); len(fails) > 0 {
			w.Header().Set("X-Tokenless-Expect-Failure", "true")
		}
	}
	s.record(Recorded{
		Endpoint: r.URL.Path,
		Provider: r.URL.Query().Get("provider"),
		Model:    req.Model,
		Scenario: name,
		Step:     step,
		Stream:   stream,
		Body:     req,
		RawBody:  raw,
	})

	if turn.Error != nil && s.consumeFailure(name, step, turn.Error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(turn.Error.Status)
		_, _ = fmt.Fprint(w, `{"error":"injected"}`)
		return
	}

	if stream {
		if turn.Stall != nil && s.consumeFailure("stall:"+name, step, &ErrorInject{Times: turn.Stall.Times}) {
			renderStalledStream(w, r, turn.Stall.Connect)
			return
		}
		s.renderStream(w, r, req.Model, step, turn)
		return
	}
	s.renderSync(w, r, req.Model, step, turn)
}

func (s *Server) record(rec Recorded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, rec)
}

// consumeFailure reports whether this request still receives the injected
// error and advances the per-scenario/step failure counter.
func (s *Server) consumeFailure(name string, step int, e *ErrorInject) bool {
	if e.Times < 0 {
		return true
	}

	key := fmt.Sprintf("%s/%d", name, step)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails[key] >= e.Times {
		return false
	}
	s.fails[key]++
	return true
}

func (s *Server) renderSync(w http.ResponseWriter, r *http.Request, model string, step int, turn Turn) {
	if !wait(r.Context(), turn.DelayMs) {
		return
	}

	msg := Message{Role: Assistant, Content: Text(turn.Content)}
	if turn.Reasoning != "" {
		reasoning := turn.Reasoning
		msg.ReasoningContent = &reasoning
	}
	if calls := turn.toolCalls(step); len(calls) > 0 {
		msg.ToolCalls = &calls
	}

	writeJSON(w, CreateChatCompletionResponse{
		ID:      "chatcmpl-mock",
		Object:  "chat.completion",
		Model:   model,
		Choices: []ChatCompletionChoice{{Index: 0, FinishReason: turn.finishReason(), Message: msg}},
		Usage:   turn.usage(),
	})
}

// renderStalledStream holds the connection open without frames until the
// client disconnects - after the initial role delta by default, or before
// the response headers when connect is true.
func renderStalledStream(w http.ResponseWriter, r *http.Request, connect bool) {
	if connect {
		<-r.Context().Done()
		return
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "tokenless: streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	sw := &streamWriter{w: w, fl: fl, ctx: r.Context()}
	if !sw.delta(ChatCompletionStreamResponseDelta{Role: Assistant}, "") {
		return
	}
	<-r.Context().Done()
}

func (s *Server) renderStream(w http.ResponseWriter, r *http.Request, model string, step int, turn Turn) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "tokenless: streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	sw := &streamWriter{w: w, fl: fl, ctx: r.Context(), model: model, delay: turn.DelayMs}
	if !sw.delta(ChatCompletionStreamResponseDelta{Role: Assistant}, "") {
		return
	}
	if turn.Malformed && !sw.raw("{this is not json") {
		return
	}
	if !streamText(sw, turn) || !streamToolCalls(sw, step, turn) {
		return
	}
	if !sw.delta(ChatCompletionStreamResponseDelta{}, turn.finishReason()) {
		return
	}
	if !sw.frame([]ChatCompletionStreamChoice{}, turn.usage()) {
		return
	}
	sw.raw("[DONE]")
}

// streamText emits reasoning fragments followed by content fragments.
func streamText(sw *streamWriter, turn Turn) bool {
	for _, c := range chunks(turn.Reasoning, turn.ChunkSize) {
		frag := c
		if !sw.delta(ChatCompletionStreamResponseDelta{ReasoningContent: &frag}, "") {
			return false
		}
	}
	for _, c := range chunks(turn.Content, turn.ChunkSize) {
		if !sw.delta(ChatCompletionStreamResponseDelta{Content: c}, "") {
			return false
		}
	}
	return true
}

// streamToolCalls emits, per tool call, a name fragment followed by at least
// two argument fragments so the client's index-keyed accumulator is always
// exercised on real multi-fragment input.
func streamToolCalls(sw *streamWriter, step int, turn Turn) bool {
	for i, tc := range turn.ToolCalls {
		id := fmt.Sprintf("call_%d_%d", step, i)
		typ := "function"
		head := ChatCompletionMessageToolCallChunk{
			Index:    i,
			ID:       &id,
			Type:     &typ,
			Function: &ChatCompletionMessageToolCallFunction{Name: tc.Name},
		}
		if !sw.toolFrame(head) {
			return false
		}

		for _, frag := range argFragments(tc.argsJSON, turn.ChunkSize) {
			part := ChatCompletionMessageToolCallChunk{
				Index:    i,
				Function: &ChatCompletionMessageToolCallFunction{Arguments: frag},
			}
			if !sw.toolFrame(part) {
				return false
			}
		}
	}
	return true
}

func (t Turn) finishReason() FinishReason {
	if len(t.ToolCalls) > 0 {
		return ToolCalls
	}
	return Stop
}

func (t Turn) usage() *CompletionUsage {
	u := Usage{PromptTokens: 10, CompletionTokens: 5}
	if t.Usage != nil {
		u = *t.Usage
	}
	usage := &CompletionUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.PromptTokens + u.CompletionTokens,
	}
	if u.CachedTokens > 0 {
		usage.PromptTokensDetails = &PromptTokensDetails{CachedTokens: &u.CachedTokens}
	}
	return usage
}

func (t Turn) toolCalls(step int) []ChatCompletionMessageToolCall {
	calls := make([]ChatCompletionMessageToolCall, len(t.ToolCalls))
	for i, tc := range t.ToolCalls {
		calls[i] = ChatCompletionMessageToolCall{
			ID:   fmt.Sprintf("call_%d_%d", step, i),
			Type: "function",
			Function: ChatCompletionMessageToolCallFunction{
				Name:      tc.Name,
				Arguments: tc.argsJSON,
			},
		}
	}
	return calls
}

// streamWriter writes SSE frames in the exact format the SDK parses:
// "data: <json>\n\n" (the space after the colon is required) with a flush
// per frame, honoring the per-frame delay and client disconnect.
type streamWriter struct {
	w     io.Writer
	fl    http.Flusher
	ctx   context.Context
	model string
	delay int
}

func (sw *streamWriter) delta(d ChatCompletionStreamResponseDelta, finish FinishReason) bool {
	return sw.frame([]ChatCompletionStreamChoice{{Index: 0, Delta: d, FinishReason: finish}}, nil)
}

func (sw *streamWriter) toolFrame(call ChatCompletionMessageToolCallChunk) bool {
	calls := []ChatCompletionMessageToolCallChunk{call}
	return sw.delta(ChatCompletionStreamResponseDelta{ToolCalls: &calls}, "")
}

func (sw *streamWriter) frame(choices []ChatCompletionStreamChoice, usage *CompletionUsage) bool {
	b, err := json.Marshal(CreateChatCompletionStreamResponse{
		ID:      "chatcmpl-mock",
		Object:  "chat.completion.chunk",
		Model:   sw.model,
		Choices: choices,
		Usage:   usage,
	})
	if err != nil {
		return false
	}
	return sw.raw(string(b))
}

func (sw *streamWriter) raw(payload string) bool {
	if !wait(sw.ctx, sw.delay) {
		return false
	}
	if _, err := fmt.Fprintf(sw.w, "data: %s\n\n", payload); err != nil {
		return false
	}
	sw.fl.Flush()
	return true
}

// wait sleeps for ms milliseconds, returning false if ctx finishes first.
func wait(ctx context.Context, ms int) bool {
	if ms <= 0 {
		return true
	}
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
		return true
	case <-ctx.Done():
		return false
	}
}

// chunks splits s into size-rune pieces (default 16), never splitting a
// UTF-8 rune across fragments.
func chunks(s string, size int) []string {
	if s == "" {
		return nil
	}
	if size <= 0 {
		size = defaultChunkSize
	}

	runes := []rune(s)
	out := make([]string, 0, len(runes)/size+1)
	for start := 0; start < len(runes); start += size {
		out = append(out, string(runes[start:min(start+size, len(runes))]))
	}
	return out
}

// argFragments splits a tool call's arguments JSON into at least two pieces
// whenever it is long enough, so accumulation is always exercised.
func argFragments(args string, size int) []string {
	if size <= 0 {
		size = defaultChunkSize
	}
	half := (len([]rune(args)) + 1) / 2
	return chunks(args, min(size, max(1, half)))
}

// handleImages answers /v1/images/generations and /v1/images/edits with a
// canned 1x1 PNG so the app's decode-and-save path runs end to end without a
// real provider. The request is recorded like a completion, so tests can
// assert the quality/size the app asked for.
func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"reading request body"}`, http.StatusBadRequest)
		return
	}
	var req CreateImageRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	model := ""
	if req.Model != nil {
		model = *req.Model
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, Recorded{
		Endpoint:   r.URL.Path,
		Provider:   r.URL.Query().Get("provider"),
		Model:      model,
		ImagesBody: &req,
		RawBody:    raw,
	})
	s.mu.Unlock()

	b64 := onePixelPNG
	writeJSON(w, ImagesResponse{Data: []Image{{B64Json: &b64}}})
}

// onePixelPNG is a base64-encoded 1x1 transparent PNG - the smallest payload
// a real image decoder accepts.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk" +
	"YPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// handleMusic answers POST /v1/audio/music with a canned audio clip selected
// by response_format (mp3 unless the request says otherwise, see
// writeAudioClip) so music tools run their save-to-disk path end to end
// without a real provider.
// duration_seconds (default 1s) drives the frame count, so a client can read
// the duration back as frames × 1152 / 44100. The request is recorded like a
// completion, so tests can assert the model, prompt, instrumental flag and
// requested format.
func (s *Server) handleMusic(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"reading request body"}`, http.StatusBadRequest)
		return
	}
	var req CreateMusicRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.reqs = append(s.reqs, Recorded{
		Endpoint:  r.URL.Path,
		Provider:  r.URL.Query().Get("provider"),
		Model:     req.Model,
		MusicBody: &req,
		RawBody:   raw,
	})
	s.mu.Unlock()

	writeAudioClip(w, req.Model, req.ResponseFormat, req.DurationSeconds)
}

// mp3Clip renders silence as identical MPEG-1 Layer III frames (44.1 kHz,
// 128 kbps, mono; zeroed side info and main data) covering durationSeconds;
// nil or non-positive means 1s.
func mp3Clip(durationSeconds *float32) []byte {
	const (
		sampleRate      = 44100
		samplesPerFrame = 1152
		frameLen        = 417
	)
	secs := 1.0
	if durationSeconds != nil && *durationSeconds > 0 {
		secs = float64(*durationSeconds)
	}
	frame := make([]byte, frameLen)
	frame[0], frame[1], frame[2], frame[3] = 0xFF, 0xFB, 0x90, 0xC0
	return slices.Repeat(frame, int(math.Ceil(secs*sampleRate/samplesPerFrame)))
}

// handleSFX answers POST /v1/audio/sfx with a canned audio clip selected by
// response_format (mp3 unless the request says otherwise, see writeAudioClip)
// so sound-effect tools run their save-to-disk path end to end without a real
// provider. The request is recorded like a completion, so tests can assert
// the model, prompt and requested format.
func (s *Server) handleSFX(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"reading request body"}`, http.StatusBadRequest)
		return
	}
	var req CreateSFXRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.reqs = append(s.reqs, Recorded{
		Endpoint: r.URL.Path,
		Provider: r.URL.Query().Get("provider"),
		Model:    req.Model,
		SFXBody:  &req,
		RawBody:  raw,
	})
	s.mu.Unlock()

	writeAudioClip(w, req.Model, req.ResponseFormat, req.DurationSeconds)
}

// wavClip renders silence as a canonical 44-byte-header 16-bit mono 44.1 kHz
// PCM WAV covering durationSeconds; nil or non-positive means 1s. The data
// section is left zeroed (silence).
func wavClip(durationSeconds *float32) []byte {
	const sampleRate = 44100
	secs := 1.0
	if durationSeconds != nil && *durationSeconds > 0 {
		secs = float64(*durationSeconds)
	}
	dataLen := uint32(math.Ceil(secs*sampleRate)) * 2
	b := make([]byte, 44+int(dataLen))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 36+dataLen)
	copy(b[8:], "WAVE")
	copy(b[12:], "fmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], sampleRate)
	binary.LittleEndian.PutUint32(b[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], dataLen)
	return b
}

// writeAudioClip writes the canned clip an audio endpoint serves, selected
// by response_format: mp3 (or unset, the spec default) gets the MPEG clip,
// pcm the same samples with the RIFF header stripped, and anything else
// (wav included - ElevenLabs cannot produce it for SFX or music, see
// inference-gateway/schemas#220) a 400 naming the format and the supported
// list. duration_seconds drives the clip length in every format.
func writeAudioClip(w http.ResponseWriter, model string, format *string, duration *float32) {
	f := "mp3"
	if format != nil {
		f = *format
	}
	switch f {
	case "mp3":
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3Clip(duration))
	case "pcm":
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(wavClip(duration)[44:])
	default:
		msg := fmt.Sprintf("%s does not support response_format %q, supported formats: mp3, pcm", model, f)
		http.Error(w, fmt.Sprintf(`{"error":%q}`, msg), http.StatusBadRequest)
	}
}

// videoJobState is one mocked video job: the served VideoJob plus the poll
// counter that walks it through the lifecycle.
type videoJobState struct {
	job   VideoJob
	polls int
}

// handleVideos answers POST /v1/videos (multipart/form-data) by recording the
// request and creating a video job in queued, mirroring createVideo. The
// returned id drives the poll/content endpoints.
func (s *Server) handleVideos(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	req := &CreateVideoRequest{Model: videoForm(r, "model"), Prompt: videoForm(r, "prompt")}
	if v := videoForm(r, "seconds"); v != "" {
		req.Seconds = &v
	}
	if v := videoForm(r, "size"); v != "" {
		req.Size = &v
	}
	if mf := r.MultipartForm; mf != nil {
		for k := range mf.Value {
			req.Fields = append(req.Fields, k)
		}
		for k := range mf.File {
			req.Fields = append(req.Fields, k)
		}
	}

	now := time.Now().Unix()
	s.mu.Lock()
	s.videoN++
	st := &videoJobState{job: VideoJob{
		ID:        fmt.Sprintf("video-job-%d", s.videoN),
		Object:    "video",
		Model:     req.Model,
		Status:    VideoQueued,
		CreatedAt: now,
		Seconds:   req.Seconds,
		Size:      req.Size,
	}}
	s.jobs[st.job.ID] = st
	s.reqs = append(s.reqs, Recorded{
		Endpoint:  r.URL.Path,
		Provider:  r.URL.Query().Get("provider"),
		Model:     req.Model,
		VideoBody: req,
	})
	s.mu.Unlock()

	writeJSON(w, st.job)
}

// videoForm returns the first value of a multipart form field, or "".
func videoForm(r *http.Request, key string) string {
	if mf := r.MultipartForm; mf != nil {
		if vs := mf.Value[key]; len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

// handleVideoJob answers GET /v1/videos/{video_id} (a poll that advances the
// job one lifecycle step) and GET /v1/videos/{video_id}/content (404 until
// the job is completed, then the canned MP4). Unknown ids 404. Both are
// recorded like other requests, so tests can assert polling behaviour.
func (s *Server) handleVideoJob(w http.ResponseWriter, r *http.Request) {
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/videos/"), "/")
	s.mu.Lock()
	st := s.jobs[id]
	s.mu.Unlock()
	s.record(Recorded{Endpoint: r.URL.Path, Provider: r.URL.Query().Get("provider")})

	if st == nil {
		http.Error(w, `{"error":"unknown video id"}`, http.StatusNotFound)
		return
	}
	switch action {
	case "":
		s.pollVideoJob(w, r, st)
	case "content":
		s.mu.Lock()
		completed := st.job.Status == VideoCompleted
		s.mu.Unlock()
		if !completed {
			http.Error(w, `{"error":"video is not completed"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(videoClip)
	default:
		http.Error(w, `{"error":"unknown video resource"}`, http.StatusNotFound)
	}
}

// pollVideoJob advances the job one step per poll - queued -> in_progress
// (rising progress) -> completed, or failed at the same poll when the
// scenario's videos.fail is set - and serves the updated job. Injected
// error/stall happen before any advance, so a failed attempt never consumes
// a lifecycle step.
func (s *Server) pollVideoJob(w http.ResponseWriter, r *http.Request, st *videoJobState) {
	cfg := s.defs.Videos
	if cfg == nil {
		cfg = &VideoConfig{}
	}
	if cfg.Error != nil && s.consumeFailure("video-poll", 0, cfg.Error) {
		w.WriteHeader(cfg.Error.Status)
		_, _ = fmt.Fprint(w, `{"error":"injected"}`)
		return
	}
	if cfg.Stall != nil && s.consumeFailure("stall:video-poll", 0, &ErrorInject{Times: cfg.Stall.Times}) {
		<-r.Context().Done()
		return
	}

	n := cfg.pollsUntilComplete()
	now := time.Now().Unix()
	s.mu.Lock()
	st.polls++
	switch {
	case cfg.Fail != nil && st.polls >= n:
		st.job.Status = VideoFailed
		st.job.CompletedAt = &now
		st.job.Error = &VideoJobError{Code: cfg.Fail.Code, Message: cfg.Fail.Message}
	case st.polls >= n:
		st.job.Status = VideoCompleted
		st.job.Progress = 100
		st.job.CompletedAt = &now
	default:
		st.job.Status = VideoInProgress
		st.job.Progress = st.polls * 100 / n
	}
	job := st.job
	s.mu.Unlock()
	writeJSON(w, job)
}

// videoClip is the canned MP4 a completed video job serves: a minimal
// Motion-JPEG-in-MP4 container with a single solid-black frame, built once
// (the video equivalent of the 1x1 PNG for images).
var videoClip = buildMP4()

// blackJPEG renders a 2x2 solid-black JPEG with the standard library - the
// single frame the canned MP4 carries. The panic is a build-time invariant
// like gateway.Default(): an in-memory *image.RGBA is always encodable.
func blackJPEG() []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		panic("tokenless: encoding canned black frame: " + err.Error())
	}
	return buf.Bytes()
}

// mp4Box assembles one ISO-BMFF box: 4-byte big-endian size, 4-byte type.
func mp4Box(name string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := make([]byte, 8, n)
	binary.BigEndian.PutUint32(b, uint32(n))
	copy(b[4:], name)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

// mp4U32 packs uint32s big-endian.
func mp4U32(vs ...uint32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.BigEndian.PutUint32(b[4*i:], v)
	}
	return b
}

// mp4U16 packs uint16s big-endian.
func mp4U16(vs ...uint16) []byte {
	b := make([]byte, 2*len(vs))
	for i, v := range vs {
		binary.BigEndian.PutUint16(b[2*i:], v)
	}
	return b
}

// buildMP4 renders the canned clip: ftyp + moov + mdat for one Motion-JPEG
// track (2x2, one sample of 1000ms) carrying a single black frame. The stco
// sample offset is patched after layout, when the mdat position is known.
func buildMP4() []byte {
	frame := blackJPEG()

	ftyp := mp4Box("ftyp", mp4U32(0x69736F6D /* isom */, 0x200, 0x69736F6D, 0x69736F32 /* iso2 */, 0x6D703431 /* mp41 */))

	mvhd := mp4Box("mvhd", []byte{0, 0, 0, 0}, // version 0, flags 0
		mp4U32(0, 0, 1000, 1000, 0x00010000), // created, modified, timescale, duration, rate 1.0
		mp4U16(0x0100, 0, 0),                 // volume, reserved
		mp4U32(0, 0),                         // reserved
		mp4U32(0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000), // unity matrix
		mp4U32(0, 0, 0, 0, 0, 0),                                     // pre_defined
		mp4U32(2),                                                    // next_track_ID
	)

	tkhd := mp4Box("tkhd", []byte{0, 0, 0, 3}, // version 0, flags 3 (enabled | in movie)
		mp4U32(0, 0, 1, 0, 1000), // created, modified, track_ID, reserved, duration
		mp4U32(0, 0),             // reserved
		mp4U16(0, 0, 0, 0),       // layer, alternate_group, volume, reserved
		mp4U32(0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000), // unity matrix
		mp4U32(2<<16, 2<<16), // width, height (16.16 fixed point)
	)

	mdhd := mp4Box("mdhd", []byte{0, 0, 0, 0},
		mp4U32(0, 0, 1000, 1000), // created, modified, timescale, duration
		mp4U16(0x55C4, 0),        // language "und", pre_defined
	)

	hdlr := mp4Box("hdlr", []byte{0, 0, 0, 0},
		mp4U32(0),       // pre_defined
		[]byte("vide"),  // handler_type
		mp4U32(0, 0, 0), // reserved
		[]byte("VideoHandler\x00"),
	)

	vmhd := mp4Box("vmhd", []byte{0, 0, 0, 1}, // version 0, flags 1
		mp4U16(0, 0, 0, 0), // graphicsmode, opcolor
	)

	url := mp4Box("url ", []byte{0, 0, 0, 1}) // flags 1: self-contained reference
	dref := mp4Box("dref", []byte{0, 0, 0, 0}, mp4U32(1), url)
	dinf := mp4Box("dinf", dref)

	// Motion-JPEG sample entry: the frame itself is a standalone JPEG, so
	// no codec-specific child box (avcC et al) is needed.
	jpegEntry := mp4Box("jpeg",
		make([]byte, 6),                // reserved
		mp4U16(1),                      // data_reference_index
		mp4U16(0, 0),                   // pre_defined, reserved
		mp4U32(0, 0, 0),                // pre_defined
		mp4U16(2, 2),                   // width, height
		mp4U32(0x00480000, 0x00480000), // 72 dpi resolution
		mp4U32(0),                      // reserved
		mp4U16(1),                      // frame_count
		make([]byte, 32),               // compressorname
		mp4U16(0x0018, 0xFFFF),         // depth 24, pre_defined -1
	)
	stsd := mp4Box("stsd", []byte{0, 0, 0, 0}, mp4U32(1), jpegEntry)
	stts := mp4Box("stts", []byte{0, 0, 0, 0}, mp4U32(1, 1, 1000))
	stsc := mp4Box("stsc", []byte{0, 0, 0, 0}, mp4U32(1, 1, 1))
	stsz := mp4Box("stsz", []byte{0, 0, 0, 0}, mp4U32(0, 1, uint32(len(frame))))
	stco := mp4Box("stco", []byte{0, 0, 0, 0}, mp4U32(1, 0)) // offset patched below
	stbl := mp4Box("stbl", stsd, stts, stsc, stsz, stco)
	minf := mp4Box("minf", vmhd, dinf, stbl)
	mdia := mp4Box("mdia", mdhd, hdlr, minf)
	trak := mp4Box("trak", tkhd, mdia)
	moov := mp4Box("moov", mvhd, trak)
	mdat := mp4Box("mdat", frame)

	out := append(append(append([]byte{}, ftyp...), moov...), mdat...)
	i := bytes.Index(out, []byte("stco"))
	binary.BigEndian.PutUint32(out[i+16:], uint32(len(ftyp)+len(moov)+8))
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// checkExpect validates the incoming request against the turn's expect block.
// It records any mismatches and returns them. The turn is still served as normal.
func (s *Server) checkExpect(scenario string, step int, endpoint string, req *CreateChatCompletionRequest, expect *ExpectBlock) []ExpectFailure {
	var fails []ExpectFailure

	if expect.Model != "" && expect.Model != req.Model {
		fails = append(fails, ExpectFailure{
			Scenario: scenario, Step: step, Field: "model",
			Want: expect.Model, Got: req.Model,
		})
	}

	if expect.Endpoint != "" && expect.Endpoint != endpoint {
		fails = append(fails, ExpectFailure{
			Scenario: scenario, Step: step, Field: "endpoint",
			Want: expect.Endpoint, Got: endpoint,
		})
	}

	if len(expect.Messages) > 0 {
		if fail := checkMessages(scenario, step, req.Messages, expect.Messages); fail != nil {
			fails = append(fails, *fail)
		}
	}

	if len(expect.ToolCalls) > 0 {
		if fail := checkToolCalls(scenario, step, req.Messages, expect.ToolCalls); fail != nil {
			fails = append(fails, *fail)
		}
	}

	if len(fails) > 0 {
		s.mu.Lock()
		s.expectFails = append(s.expectFails, fails...)
		s.mu.Unlock()
	}
	return fails
}

// checkMessages checks that expected messages appear as an ordered subsequence.
func checkMessages(scenario string, step int, actual []Message, expected []ExpectMessage) *ExpectFailure {
	ei := 0
	for _, a := range actual {
		if ei >= len(expected) {
			break
		}
		e := expected[ei]
		if string(a.Role) == e.Role && strings.Contains(a.Content.Text(), e.Content) {
			ei++
		}
	}
	if ei < len(expected) {
		return &ExpectFailure{
			Scenario: scenario, Step: step, Field: "messages",
			Want: expected[ei].Role + ":" + expected[ei].Content,
			Got:  "not found in order",
		}
	}
	return nil
}

// checkToolCalls checks that the most recent assistant message has the expected
// tool calls in order, with deep partial match on args.
func checkToolCalls(scenario string, step int, messages []Message, expected []ExpectToolCall) *ExpectFailure {
	// Find the most recent assistant message with tool calls.
	var lastToolMsg *Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == Assistant && messages[i].ToolCalls != nil {
			lastToolMsg = &messages[i]
			break
		}
	}
	if lastToolMsg == nil {
		return &ExpectFailure{
			Scenario: scenario, Step: step, Field: "tool_calls",
			Want: fmt.Sprintf("%d tool calls", len(expected)),
			Got:  "no assistant message with tool calls",
		}
	}

	calls := *lastToolMsg.ToolCalls
	if len(calls) < len(expected) {
		return &ExpectFailure{
			Scenario: scenario, Step: step, Field: "tool_calls",
			Want: fmt.Sprintf("%d tool calls", len(expected)),
			Got:  fmt.Sprintf("%d tool calls", len(calls)),
		}
	}

	for i, e := range expected {
		a := calls[i]
		if a.Function.Name != e.Name {
			return &ExpectFailure{
				Scenario: scenario, Step: step, Field: "tool_calls",
				Want: fmt.Sprintf("tool_calls[%d].name=%s", i, e.Name),
				Got:  fmt.Sprintf("tool_calls[%d].name=%s", i, a.Function.Name),
			}
		}
		if len(e.Args) > 0 {
			var actualArgs map[string]any
			if err := json.Unmarshal([]byte(a.Function.Arguments), &actualArgs); err != nil {
				return &ExpectFailure{
					Scenario: scenario, Step: step, Field: "tool_calls",
					Want: fmt.Sprintf("tool_calls[%d].args=%v", i, e.Args),
					Got:  fmt.Sprintf("invalid JSON: %s", a.Function.Arguments),
				}
			}
			for k, wantV := range e.Args {
				gotV, ok := actualArgs[k]
				if !ok {
					return &ExpectFailure{
						Scenario: scenario, Step: step, Field: "tool_calls",
						Want: fmt.Sprintf("tool_calls[%d].args.%s=%v", i, k, wantV),
						Got:  fmt.Sprintf("key %q missing", k),
					}
				}
				wantJSON, _ := json.Marshal(wantV)
				gotJSON, _ := json.Marshal(gotV)
				if string(wantJSON) != string(gotJSON) {
					return &ExpectFailure{
						Scenario: scenario, Step: step, Field: "tool_calls",
						Want: fmt.Sprintf("tool_calls[%d].args.%s=%s", i, k, wantJSON),
						Got:  fmt.Sprintf("tool_calls[%d].args.%s=%s", i, k, gotJSON),
					}
				}
			}
		}
	}
	return nil
}

// handleExpect serves the GET /v1/expect endpoint: a JSON report of recorded
// expectation failures. Status 200 when clean, 412 when any failure exists.
func (s *Server) handleExpect(w http.ResponseWriter, r *http.Request) {
	fails := s.ExpectFailures()
	status := http.StatusOK
	if len(fails) > 0 {
		status = http.StatusPreconditionFailed
	}
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"failures": fails})
}

// projectMessages converts an Anthropic messages request into the chat
// completions shape for expect checking.
func (s *Server) projectMessages(req *CreateMessagesRequest) *CreateChatCompletionRequest {
	projected := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		text := messagesText(m.Content)
		if m.Role == MessagesRoleUser && text == "" {
			continue
		}
		role := User
		if m.Role == MessagesRoleAssistant {
			role = Assistant
		}
		projected = append(projected, Message{Role: role, Content: Text(text)})
	}
	return &CreateChatCompletionRequest{Model: req.Model, Messages: projected}
}
