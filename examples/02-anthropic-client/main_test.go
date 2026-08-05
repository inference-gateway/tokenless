// Testing an app built on the official Anthropic Go client: point the client's
// base URL at tokenless via StartMock and nothing else changes. The gateway
// serves the Anthropic-native /v1/messages endpoint, sync and SSE streaming.
package main

import (
	"context"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	option "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
)

func TestAnthropicClientSync(t *testing.T) {
	mock := tokenless.StartMock(t)

	client := anthropic.NewClient(
		option.WithBaseURL(mock.URL),
		option.WithAPIKey("tokenless"),
	)

	params := anthropic.MessageNewParams{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("say hello")),
		},
	}

	resp, err := client.Messages.New(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, resp.Content, 1)
	text, ok := resp.Content[0].AsAny().(anthropic.TextBlock)
	require.True(t, ok)
	require.Equal(t, "Hello! How can I help?", text.Text)
}

func TestAnthropicClientStream(t *testing.T) {
	mock := tokenless.StartMock(t)

	client := anthropic.NewClient(
		option.WithBaseURL(mock.URL),
		option.WithAPIKey("tokenless"),
	)

	params := anthropic.MessageNewParams{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("say hello")),
		},
	}

	stream := client.Messages.NewStreaming(context.Background(), params)
	var streamed string
	for stream.Next() {
		if delta, ok := stream.Current().AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok {
				streamed += text.Text
			}
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, "Hello! How can I help?", streamed)
}
