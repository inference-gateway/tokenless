// Command cobra-agent is a tiny cobra CLI that asks an OpenAI-compatible
// gateway a question - the kind of app you would test with tokenless. The
// gateway URL comes from AGENT_GATEWAY_URL, so a test points it at the mock
// and the production binary points it at the real thing; the code cannot
// tell the difference.
//
//	AGENT_GATEWAY_URL=http://127.0.0.1:8080/v1 go run . ask "say hello"
package main

import (
	"context"
	"fmt"
	"os"

	openai "github.com/openai/openai-go/v3"
	option "github.com/openai/openai-go/v3/option"
	cobra "github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{Use: "cobra-agent", SilenceUsage: true}

	var model string
	ask := &cobra.Command{
		Use:   "ask [prompt]",
		Short: "Send one prompt to the gateway and print the answer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			baseURL := os.Getenv("AGENT_GATEWAY_URL")
			if baseURL == "" {
				return fmt.Errorf("AGENT_GATEWAY_URL is not set")
			}

			client := openai.NewClient(
				option.WithBaseURL(baseURL),
				option.WithAPIKey("tokenless"),
			)
			resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
				Model:    openai.ChatModel(model),
				Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(args[0])},
			})
			if err != nil {
				return err
			}

			fmt.Println(resp.Choices[0].Message.Content)
			return nil
		},
	}
	ask.Flags().StringVarP(&model, "model", "m", "gpt-4o", "model id")
	root.AddCommand(ask)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
