// Testing an app built on the inference-gateway Go SDK: same trick, the
// client's BaseURL points at tokenless.
//
//	go run ./sdk
package main

import (
	"context"
	"fmt"
	"net/http/httptest"

	sdk "github.com/inference-gateway/sdk"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

func main() {
	ts := httptest.NewServer(gateway.New())
	defer ts.Close()

	client := sdk.NewClient(&sdk.ClientOptions{BaseURL: ts.URL + "/v1"})
	ctx := context.Background()

	models, err := client.ListModels(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println("models:", len(models.Data))

	msg, err := sdk.NewTextMessage(sdk.User, "say hello")
	if err != nil {
		panic(err)
	}
	resp, err := client.GenerateContent(ctx, sdk.Openai, "gpt-4o", []sdk.Message{msg})
	if err != nil {
		panic(err)
	}
	content, err := resp.Choices[0].Message.Content.AsMessageContent0()
	if err != nil {
		panic(err)
	}
	fmt.Println("sync:", content)
}
