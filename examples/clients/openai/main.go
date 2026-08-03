// Testing an app built on the official OpenAI Go client: point the client's
// base URL at tokenless and nothing else changes. Your production code keeps
// constructing the client from a config value; the test sets that value to
// the mock's URL.
//
//	go run ./openai
package main

import (
	"context"
	"fmt"
	"net/http/httptest"

	openai "github.com/openai/openai-go/v3"
	option "github.com/openai/openai-go/v3/option"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

func main() {
	ts := httptest.NewServer(gateway.New())
	defer ts.Close()

	client := openai.NewClient(
		option.WithBaseURL(ts.URL+"/v1"),
		option.WithAPIKey("tokenless"),
	)

	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("say hello")},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("sync:", resp.Choices[0].Message.Content)

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
	if err := stream.Err(); err != nil {
		panic(err)
	}
	fmt.Println("streamed:", streamed)
}
