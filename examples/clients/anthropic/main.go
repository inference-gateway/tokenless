// Testing an app built on the official Anthropic Go client: point the client's
// base URL at tokenless and nothing else changes. The gateway serves the
// Anthropic-native /v1/messages endpoint, sync and SSE streaming.
//
//	go run ./anthropic
package main

import (
	"context"
	"fmt"
	"net/http/httptest"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	option "github.com/anthropics/anthropic-sdk-go/option"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

func main() {
	ts := httptest.NewServer(gateway.New())
	defer ts.Close()

	client := anthropic.NewClient(
		option.WithBaseURL(ts.URL),
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
	if err != nil {
		panic(err)
	}
	for _, block := range resp.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			fmt.Println("sync:", text.Text)
		}
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
	if err := stream.Err(); err != nil {
		panic(err)
	}
	fmt.Println("streamed:", streamed)
}
