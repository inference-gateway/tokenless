// Posting to /v1/images/generations and decoding the returned base64 PNG.
// The mock always returns a canned 1x1 transparent PNG so the app's
// decode-and-save path runs end to end without a real provider.
package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
	gateway "github.com/inference-gateway/tokenless/gateway"
)

func TestImageGeneration(t *testing.T) {
	mock := tokenless.StartMock(t)

	body := `{"model":"gpt-image-2","prompt":"a cat","n":1,"size":"1024x1024"}`
	resp, err := http.Post(mock.URL+"/v1/images/generations", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, 200, resp.StatusCode)

	var imgResp gateway.ImagesResponse
	b, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(b, &imgResp))
	require.Len(t, imgResp.Data, 1)
	require.NotNil(t, imgResp.Data[0].B64Json)

	// Decode the base64 PNG and verify it's valid.
	pngData, err := base64.StdEncoding.DecodeString(*imgResp.Data[0].B64Json)
	require.NoError(t, err)
	require.True(t, len(pngData) > 0, "decoded PNG data is empty")

	// PNG header: 89 50 4E 47 0D 0A 1A 0A
	require.Equal(t, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, pngData[:8],
		"not a valid PNG header")
}

func TestImageGenerationRecordsRequest(t *testing.T) {
	mock := tokenless.StartMock(t)

	body := `{"model":"gpt-image-2","prompt":"a dog","n":2,"size":"512x512","quality":"standard"}`
	resp, err := http.Post(mock.URL+"/v1/images/generations", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()

	recs := mock.Requests()
	require.Len(t, recs, 1)
	require.Equal(t, "/v1/images/generations", recs[0].Endpoint)
	require.NotNil(t, recs[0].ImagesBody)
	require.Equal(t, "a dog", recs[0].ImagesBody.Prompt)
	require.NotNil(t, recs[0].ImagesBody.N)
	require.Equal(t, 2, *recs[0].ImagesBody.N)
}
