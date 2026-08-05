// Defining a custom models: section in a scenario file and asserting the
// custom list is served on GET /v1/models. This lets apps test model
// filtering, pricing display, and modality-aware routing.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
	gateway "github.com/inference-gateway/tokenless/gateway"
)

func TestCustomModelList(t *testing.T) {
	defs, err := gateway.Load([]byte(`
models:
  - id: my-custom-model
    object: model
    owned_by: me
    served_by: my-server
    context_window: { tokens: 4096, source: "custom" }
    pricing:
      input_per_token: "0.000001"
      output_per_token: "0.000004"
      currency: "USD"
      source: "custom"
    modalities: [text]
  - id: my-vision-model
    object: model
    owned_by: me
    served_by: my-server
    modalities: [text, image]
fallback:
  content: "Done."
scenarios:
  - name: ping
    match: '(?i)ping'
    turns:
      - content: "pong"
`))
	require.NoError(t, err)
	mock := tokenless.StartMock(t, defs)

	resp, err := http.Get(mock.URL + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, 200, resp.StatusCode)

	var models gateway.ListModelsResponse
	b, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(b, &models))
	require.Len(t, models.Data, 2)

	require.Equal(t, "my-custom-model", models.Data[0].ID)
	require.Equal(t, "me", models.Data[0].OwnedBy)
	require.NotNil(t, models.Data[0].ContextWindow)
	require.Equal(t, 4096, models.Data[0].ContextWindow.Tokens)
	require.NotNil(t, models.Data[0].Pricing)
	require.Equal(t, "0.000001", models.Data[0].Pricing.InputPerToken)
	require.Len(t, models.Data[0].Modalities, 1)
	require.Equal(t, gateway.ModelModalities("text"), models.Data[0].Modalities[0])

	require.Equal(t, "my-vision-model", models.Data[1].ID)
	require.Len(t, models.Data[1].Modalities, 2)
}
