// Testing an app built on the inference-gateway Go SDK: same trick, the
// client's BaseURL points at tokenless via StartMock.
package main

import (
	"context"
	"testing"

	sdk "github.com/inference-gateway/sdk"
	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
)

func TestSDKClientModels(t *testing.T) {
	mock := tokenless.StartMock(t)

	client := sdk.NewClient(&sdk.ClientOptions{BaseURL: mock.URL + "/v1"})

	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	require.Greater(t, len(models.Data), 0)
}

func TestSDKClientChat(t *testing.T) {
	mock := tokenless.StartMock(t)

	client := sdk.NewClient(&sdk.ClientOptions{BaseURL: mock.URL + "/v1"})

	msg, err := sdk.NewTextMessage(sdk.User, "say hello")
	require.NoError(t, err)

	resp, err := client.GenerateContent(context.Background(), sdk.Openai, "gpt-4o", []sdk.Message{msg})
	require.NoError(t, err)
	content, err := resp.Choices[0].Message.Content.AsMessageContent0()
	require.NoError(t, err)
	require.Equal(t, "Hello! How can I help?", content)
}
