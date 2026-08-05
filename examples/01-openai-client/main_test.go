// Testing an app built on the official OpenAI Go client: point the client's
// base URL at tokenless via StartMock and nothing else changes. Your
// production code keeps constructing the client from a config value; the test
// sets that value to the mock's URL.
package main

import (
	"context"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
	option "github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
)

func TestOpenAIClientSync(t *testing.T) {
	mock := tokenless.StartMock(t)

	client := openai.NewClient(
		option.WithBaseURL(mock.URL+"/v1"),
		option.WithAPIKey("tokenless"),
	)

	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("say hello")},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello! How can I help?", resp.Choices[0].Message.Content)
}

func TestOpenAIClientStream(t *testing.T) {
	mock := tokenless.StartMock(t)

	client := openai.NewClient(
		option.WithBaseURL(mock.URL+"/v1"),
		option.WithAPIKey("tokenless"),
	)

	stream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("say hello")},
	})
	var streamed string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			streamed += chunk.Choices[0].Delta.Content
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, "Hello! How can I help?", streamed)
}
