// Wire types for the OpenAI-compatible and Anthropic-compatible surfaces the
// mock serves. Hand-written (not code-generated) and deliberately minimal:
// only the fields the mock reads or writes exist; unknown request fields are
// ignored by encoding/json. JSON tags follow the OpenAI / Anthropic wire
// formats plus the gateway extensions (served_by, context_window, pricing).
package gateway

import (
	"encoding/json"
	"strings"
)

// MessageRole is the sender role in an OpenAI-shaped message.
type MessageRole string

const (
	User      MessageRole = "user"
	Assistant MessageRole = "assistant"
	Tool      MessageRole = "tool"
	System    MessageRole = "system"
)

// FinishReason is why the model stopped generating.
type FinishReason string

const (
	Stop      FinishReason = "stop"
	ToolCalls FinishReason = "tool_calls"
)

// MessageContent is the OpenAI content union: a bare string or an array of
// content parts. The raw JSON is kept verbatim so recorded requests
// round-trip; Text extracts the textual content either way.
type MessageContent struct {
	raw json.RawMessage
}

// Text returns a MessageContent holding a bare string.
func Text(s string) MessageContent {
	b, _ := json.Marshal(s)
	return MessageContent{raw: b}
}

func (c MessageContent) MarshalJSON() ([]byte, error) {
	if c.raw == nil {
		return []byte(`""`), nil
	}
	return c.raw, nil
}

func (c *MessageContent) UnmarshalJSON(b []byte) error {
	c.raw = append(json.RawMessage(nil), b...)
	return nil
}

// Text returns the bare string form, or the concatenated text parts of a
// multimodal content array.
func (c MessageContent) Text() string {
	var s string
	if err := json.Unmarshal(c.raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(c.raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Message is one OpenAI-shaped chat message.
type Message struct {
	Role             MessageRole                      `json:"role"`
	Content          MessageContent                   `json:"content"`
	ReasoningContent *string                          `json:"reasoning_content,omitempty"`
	ToolCallID       *string                          `json:"tool_call_id,omitempty"`
	ToolCalls        *[]ChatCompletionMessageToolCall `json:"tool_calls,omitempty"`
}

// ChatCompletionMessageToolCall is a completed tool call on a message.
type ChatCompletionMessageToolCall struct {
	ID       string                                `json:"id"`
	Type     string                                `json:"type"`
	Function ChatCompletionMessageToolCallFunction `json:"function"`
}

// ChatCompletionMessageToolCallFunction is the function name/arguments pair.
type ChatCompletionMessageToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ChatCompletionMessageToolCallChunk is one streamed tool-call fragment.
type ChatCompletionMessageToolCallChunk struct {
	Index    int                                    `json:"index"`
	ID       *string                                `json:"id,omitempty"`
	Type     *string                                `json:"type,omitempty"`
	Function *ChatCompletionMessageToolCallFunction `json:"function,omitempty"`
}

// CreateChatCompletionRequest is the decoded POST /v1/chat/completions body.
type CreateChatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   *bool     `json:"stream,omitempty"`
}

// ChatCompletionChoice is one sync-response choice.
type ChatCompletionChoice struct {
	Index        int          `json:"index"`
	FinishReason FinishReason `json:"finish_reason"`
	Message      Message      `json:"message"`
}

// CreateChatCompletionResponse is the sync chat.completion body.
type CreateChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int                    `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *CompletionUsage       `json:"usage,omitempty"`
}

// ChatCompletionStreamResponseDelta is the delta inside a stream choice.
type ChatCompletionStreamResponseDelta struct {
	Role             MessageRole                           `json:"role"`
	Content          string                                `json:"content"`
	ReasoningContent *string                               `json:"reasoning_content,omitempty"`
	ToolCalls        *[]ChatCompletionMessageToolCallChunk `json:"tool_calls,omitempty"`
}

// ChatCompletionStreamChoice is one chunk choice.
type ChatCompletionStreamChoice struct {
	Index        int                               `json:"index"`
	Delta        ChatCompletionStreamResponseDelta `json:"delta"`
	FinishReason FinishReason                      `json:"finish_reason"`
}

// CreateChatCompletionStreamResponse is one chat.completion.chunk frame.
type CreateChatCompletionStreamResponse struct {
	ID      string                       `json:"id"`
	Object  string                       `json:"object"`
	Created int                          `json:"created"`
	Model   string                       `json:"model"`
	Choices []ChatCompletionStreamChoice `json:"choices"`
	Usage   *CompletionUsage             `json:"usage,omitempty"`
}

// PromptTokensDetails is the cached-token breakdown of a prompt.
type PromptTokensDetails struct {
	AudioTokens  *int64 `json:"audio_tokens,omitempty"`
	CachedTokens *int64 `json:"cached_tokens,omitempty"`
}

// CompletionUsage is the OpenAI-shaped token usage block.
type CompletionUsage struct {
	PromptTokens        int64                `json:"prompt_tokens"`
	CompletionTokens    int64                `json:"completion_tokens"`
	TotalTokens         int64                `json:"total_tokens"`
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// Pricing is the gateway's per-token pricing extension on /v1/models.
type Pricing struct {
	InputPerToken      string  `json:"input_per_token"`
	OutputPerToken     string  `json:"output_per_token"`
	CacheReadPerToken  *string `json:"cache_read_per_token,omitempty"`
	CacheWritePerToken *string `json:"cache_write_per_token,omitempty"`
	Currency           string  `json:"currency"`
	Source             string  `json:"source"`
}

// ContextWindow is the gateway's context-window extension on /v1/models.
type ContextWindow struct {
	Tokens int    `json:"tokens"`
	Source string `json:"source"`
}

// ModelModalities is a modality a model supports (e.g. "text", "image").
type ModelModalities string

// Model is one /v1/models entry.
type Model struct {
	ID            string             `json:"id"`
	Object        string             `json:"object"`
	Created       int64              `json:"created"`
	OwnedBy       string             `json:"owned_by"`
	ServedBy      string             `json:"served_by"`
	ContextWindow *ContextWindow     `json:"context_window,omitempty"`
	Pricing       *Pricing           `json:"pricing,omitempty"`
	Modalities    []ModelModalities  `json:"modalities,omitempty"`
}

// ListModelsResponse is the /v1/models body.
type ListModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// CreateImageRequest is the decoded /v1/images/generations (or edits) body.
type CreateImageRequest struct {
	Prompt         string  `json:"prompt"`
	Model          *string `json:"model,omitempty"`
	N              *int    `json:"n,omitempty"`
	Size           *string `json:"size,omitempty"`
	Quality        *string `json:"quality,omitempty"`
	ResponseFormat *string `json:"response_format,omitempty"`
}

// Image is one generated image.
type Image struct {
	B64Json       *string `json:"b64_json,omitempty"`
	URL           *string `json:"url,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
}

// ImagesResponse is the images endpoint body.
type ImagesResponse struct {
	Created int64   `json:"created"`
	Data    []Image `json:"data"`
}

// MessagesMessageRole is the sender role in an Anthropic message.
type MessagesMessageRole string

const (
	MessagesRoleUser      MessagesMessageRole = "user"
	MessagesRoleAssistant MessagesMessageRole = "assistant"
)

// MessagesContent is the Anthropic content union: a bare string or an array
// of content blocks.
type MessagesContent struct {
	raw json.RawMessage
}

func (c MessagesContent) MarshalJSON() ([]byte, error) {
	if c.raw == nil {
		return []byte(`""`), nil
	}
	return c.raw, nil
}

func (c *MessagesContent) UnmarshalJSON(b []byte) error {
	c.raw = append(json.RawMessage(nil), b...)
	return nil
}

// Texts returns the textual pieces of the content: the bare string as a
// single element, or the text of every text block in a block array.
func (c MessagesContent) Texts() []string {
	var s string
	if err := json.Unmarshal(c.raw, &s); err == nil {
		return []string{s}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(c.raw, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

// MessagesMessage is one Anthropic-shaped message.
type MessagesMessage struct {
	Role    MessagesMessageRole `json:"role"`
	Content MessagesContent     `json:"content"`
}

// CreateMessagesRequest is the decoded POST /v1/messages body.
type CreateMessagesRequest struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	Messages  []MessagesMessage `json:"messages"`
	Stream    *bool             `json:"stream,omitempty"`
}
