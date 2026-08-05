package gateway

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

//go:embed scenarios.yaml
var embeddedScenarios []byte

// injectableStatuses are the HTTP statuses allowed for error injection. 400
// is deliberately excluded: many agent HTTP clients treat it as retryable,
// which would turn an intended hard failure into a surprising retry storm
// for scenario authors.
var injectableStatuses = map[int]bool{408: true, 429: true, 500: true, 502: true, 503: true, 504: true}

// ToolDef declares how to execute a tool: either as a Go-registered func
// (test-side) or via an exec command (standalone binary / non-Go users).
// Exec is an argv template: each element is a Go text/template over the
// tool call's JSON args, e.g. ["cat", "{{.path}}"].
type ToolDef struct {
	Exec []string `yaml:"exec"`
}

// ScenarioFile is the root of a scenarios YAML document.
type ScenarioFile struct {
	// Fallback is rendered when no scenario matches the prompt or when a
	// matched scenario has no turn left for the current step.
	Fallback Turn `yaml:"fallback"`
	// Scenarios are evaluated in file order; the first regex match wins.
	Scenarios []Scenario `yaml:"scenarios"`
	// Models, when set, replaces the hardcoded /v1/models response with the
	// given model list. When nil or empty the built-in model list is used.
	Models []Model `yaml:"models,omitempty"`
	// Tools maps tool names to their exec definitions. Opt-in; without it
	// behavior is unchanged (tool results come from whatever the client sends).
	Tools map[string]*ToolDef `yaml:"tools,omitempty"`
}

// Scenario is one scripted conversation, selected by regex and optionally
// constrained to a specific model.
type Scenario struct {
	// Name uniquely identifies the scenario in recordings and logs.
	Name string `yaml:"name"`
	// Match is a Go regular expression tested (unanchored) against the latest
	// user message of each request that is not an injected <system-reminder>.
	Match string `yaml:"match"`
	// Model, when set, constrains this scenario to only match requests whose
	// model field equals this value (exact string match).
	Model string `yaml:"model,omitempty"`
	// Turns are the scripted assistant responses, indexed by the number of
	// assistant messages following the matched user message.
	Turns []Turn `yaml:"turns"`

	re *regexp.Regexp
}

// Turn is one scripted assistant response, rendered as SSE or as a sync JSON
// body depending on the request's stream flag.
type Turn struct {
	// Content is the assistant text, streamed in ChunkSize-rune fragments.
	Content string `yaml:"content"`
	// Reasoning is streamed as reasoning_content deltas before Content.
	Reasoning string `yaml:"reasoning"`
	// ToolCalls all land in this single assistant turn.
	ToolCalls []ToolCall `yaml:"tool_calls"`
	// Usage defaults to 10 prompt / 5 completion tokens when nil.
	Usage *Usage `yaml:"usage"`
	// ChunkSize is the fragment size in runes for streamed text (default 16).
	ChunkSize int `yaml:"chunk_size"`
	// DelayMs sleeps before each SSE frame (streaming) or once before the
	// body (sync), aborting early when the client disconnects.
	DelayMs int `yaml:"delay_ms"`
	// Error, when set, replaces the turn with an HTTP error for the first
	// Times matching requests (-1 means every request).
	Error *ErrorInject `yaml:"error"`
	// Stall, when set, makes the first Times streaming requests hang after
	// the initial role delta: the connection stays open but no further
	// frames arrive until the client disconnects. Exercises the app's
	// stalled-stream reconnect path.
	Stall *StallInject `yaml:"stall"`
	// Malformed emits one non-JSON data: frame early in the stream.
	Malformed bool `yaml:"malformed"`
	// Expect validates the incoming request that resolved to this turn.
	// A mismatch is recorded but the turn is still served as normal.
	Expect *ExpectBlock `yaml:"expect"`
}

// ToolCall describes one function call the mock model requests.
type ToolCall struct {
	// Name is the tool name as registered in the app (e.g. Read, Grep, Bash).
	Name string `yaml:"name"`
	// Args is marshaled into the tool call's JSON arguments string.
	Args map[string]any `yaml:"args"`

	argsJSON string
}

// ExpectBlock declares what the incoming request that resolved to this turn
// must look like. All fields are optional; only set fields are checked.
// Matching is tolerant: messages is an ordered subsequence match, tool_calls
// is a deep partial match on args (expected keys must be present and equal,
// extra keys allowed).
type ExpectBlock struct {
	// Model is an exact string match against the request model.
	Model string `yaml:"model,omitempty"`
	// Endpoint is an exact string match against the request path.
	Endpoint string `yaml:"endpoint,omitempty"`
	// ToolCalls matches the request's most recent assistant message, ordered,
	// with deep partial match on args.
	ToolCalls []ExpectToolCall `yaml:"tool_calls,omitempty"`
	// Messages is an ordered subsequence match: each expected message must
	// appear, in order, with exact role and substring content; extra messages
	// are allowed.
	Messages []ExpectMessage `yaml:"messages,omitempty"`
}

// ExpectToolCall is one expected tool call in an expect block.
type ExpectToolCall struct {
	Name string         `yaml:"name"`
	Args map[string]any `yaml:"args,omitempty"`
}

// ExpectMessage is one expected message in an expect block.
type ExpectMessage struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

// Usage overrides the token usage reported for a turn. CacheWriteTokens is
// only surfaced on the /v1/messages endpoint (cache_creation_input_tokens);
// the OpenAI-shaped usage has no field for it.
type Usage struct {
	PromptTokens     int64 `yaml:"prompt_tokens"`
	CompletionTokens int64 `yaml:"completion_tokens"`
	CachedTokens     int64 `yaml:"cached_tokens"`
	CacheWriteTokens int64 `yaml:"cache_write_tokens"`
}

// ErrorInject makes a turn answer with HTTP Status for the first Times
// requests that resolve to it; Times -1 fails forever.
type ErrorInject struct {
	Status int `yaml:"status"`
	Times  int `yaml:"times"`
}

// StallInject makes a turn's stream hang for the first Times requests that
// resolve to it; Times -1 stalls forever. By default the stream stalls after
// the initial role delta; with Connect true it stalls before the response
// headers, simulating a TCP connect that never completes.
type StallInject struct {
	Times   int  `yaml:"times"`
	Connect bool `yaml:"connect"`
}

// Default returns the embedded built-in scenario library.
func Default() *ScenarioFile {
	f, err := Load(embeddedScenarios)
	if err != nil {
		panic(fmt.Sprintf("tokenless: embedded scenarios.yaml is invalid: %v", err))
	}
	return f
}

// Load parses and validates a scenarios YAML document. Unknown fields are
// rejected so typos in scenario files fail fast.
func Load(b []byte) (*ScenarioFile, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)

	var f ScenarioFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parsing scenarios: %w", err)
	}
	if err := f.validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// LoadFile parses and validates a scenarios YAML document from a file path.
// This is the entry point for apps that ship their own scenario library.
func LoadFile(path string) (*ScenarioFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading scenarios: %w", err)
	}
	f, err := Load(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

func (f *ScenarioFile) validate() error {
	if err := f.Fallback.validate("fallback"); err != nil {
		return err
	}

	for name, def := range f.Tools {
		if len(def.Exec) == 0 {
			return fmt.Errorf("tool %q: exec is required", name)
		}
	}

	seen := make(map[string]bool, len(f.Scenarios))
	for i := range f.Scenarios {
		if err := f.Scenarios[i].validate(seen); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scenario) validate(seen map[string]bool) error {
	if s.Name == "" {
		return fmt.Errorf("scenario with match %q: name is required", s.Match)
	}
	if seen[s.Name] {
		return fmt.Errorf("scenario %q: duplicate name", s.Name)
	}
	seen[s.Name] = true

	if s.Match == "" {
		return fmt.Errorf("scenario %q: match is required", s.Name)
	}
	re, err := regexp.Compile(s.Match)
	if err != nil {
		return fmt.Errorf("scenario %q: invalid match: %w", s.Name, err)
	}
	s.re = re

	if len(s.Turns) == 0 {
		return fmt.Errorf("scenario %q: at least one turn is required", s.Name)
	}
	for i := range s.Turns {
		if err := s.Turns[i].validate(fmt.Sprintf("scenario %q turn %d", s.Name, i)); err != nil {
			return err
		}
	}
	return nil
}

func (t *Turn) validate(where string) error {
	if t.ChunkSize < 0 || t.DelayMs < 0 {
		return fmt.Errorf("%s: chunk_size and delay_ms must be >= 0", where)
	}
	if t.Expect != nil {
		if err := t.Expect.validate(where); err != nil {
			return err
		}
	}
	if t.Error != nil {
		if !injectableStatuses[t.Error.Status] {
			return fmt.Errorf("%s: error.status must be one of 408, 429, 500, 502, 503, 504", where)
		}
		if t.Error.Times == 0 || t.Error.Times < -1 {
			return fmt.Errorf("%s: error.times must be positive or -1", where)
		}
	}
	if t.Stall != nil && (t.Stall.Times == 0 || t.Stall.Times < -1) {
		return fmt.Errorf("%s: stall.times must be positive or -1", where)
	}

	for i := range t.ToolCalls {
		tc := &t.ToolCalls[i]
		if tc.Name == "" {
			return fmt.Errorf("%s: tool_calls[%d].name is required", where, i)
		}
		if tc.Args == nil {
			tc.argsJSON = "{}"
			continue
		}
		args, err := json.Marshal(tc.Args)
		if err != nil {
			return fmt.Errorf("%s: tool_calls[%d].args: %w", where, i, err)
		}
		tc.argsJSON = string(args)
	}
	return nil
}

func (e *ExpectBlock) validate(where string) error {
	for i := range e.ToolCalls {
		tc := &e.ToolCalls[i]
		if tc.Name == "" {
			return fmt.Errorf("%s: expect.tool_calls[%d].name is required", where, i)
		}
	}
	for i := range e.Messages {
		m := &e.Messages[i]
		if m.Role == "" {
			return fmt.Errorf("%s: expect.messages[%d].role is required", where, i)
		}
	}
	return nil
}

// resolve picks the scenario and turn for a request. It returns the matched
// scenario name ("" when only the fallback applies), the step derived from
// the assistant-message count after the anchor, and the turn to render.
func (f *ScenarioFile) resolve(req *CreateChatCompletionRequest) (string, int, Turn) {
	prompt, anchor := anchorUserMessage(req.Messages)

	step := 0
	for _, m := range req.Messages[anchor+1:] {
		if m.Role == Assistant {
			step++
		}
	}

	for i := range f.Scenarios {
		sc := &f.Scenarios[i]
		if !sc.re.MatchString(prompt) {
			continue
		}
		if sc.Model != "" && sc.Model != req.Model {
			continue
		}
		if step < len(sc.Turns) {
			return sc.Name, step, sc.Turns[step]
		}
		return sc.Name, step, f.Fallback
	}
	return "", step, f.Fallback
}

// resolveMessages resolves a /v1/messages request against the same scenario
// library by projecting the Anthropic messages onto the chat-completions
// shape resolve understands: text is extracted from string content or text
// blocks, and tool_result-only user messages are dropped so they can't
// anchor a scenario.
func (f *ScenarioFile) resolveMessages(req *CreateMessagesRequest) (string, int, Turn) {
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
	return f.resolve(&CreateChatCompletionRequest{Model: req.Model, Messages: projected})
}

// messagesText extracts the concatenated text of an Anthropic message
// content union: a bare string or the text blocks of a block array.
// <system-reminder> blocks are skipped: the adapter merges the volatile tail
// into the same user message as the prompt, and including it would make
// anchorUserMessage reject the whole message.
func messagesText(c MessagesContent) string {
	var b strings.Builder
	for _, text := range c.Texts() {
		if strings.Contains(text, "<system-reminder>") {
			continue
		}
		b.WriteString(text)
	}
	return b.String()
}

// jobNoticeRe matches the background-job completion notices agent harnesses
// inject as synthetic user messages.
var jobNoticeRe = regexp.MustCompile(`^\[(A2A Task|Background Shell|Subagent|Background Job) (Completed|Failed|Awaiting Approval): `)

// anchorUserMessage returns the text and index of the latest user message that
// is not injected harness content (<system-reminder> or a background-job
// completion notice). Anchoring on the latest real prompt lets an interactive chat session
// re-route to a new scenario on every message, while the headless loop's
// automated-check reminder and background-job notices never re-route a run
// mid-loop. Steps count the assistant messages after the anchor, so each new
// prompt restarts its scenario at turn 0.
func anchorUserMessage(messages []Message) (string, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != User {
			continue
		}
		text := messages[i].Content.Text()
		if strings.Contains(text, "<system-reminder>") || jobNoticeRe.MatchString(text) {
			continue
		}
		return text, i
	}
	return "", -1
}
