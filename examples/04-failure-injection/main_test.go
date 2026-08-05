// Exercising the built-in failure injection scenarios: 429 retries, 500 hard
// failures, stalled streams, stalled connects, and malformed SSE. Each test
// drives the mock directly and asserts on the HTTP response.
package main

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
)

func TestFailureInjectionRetry(t *testing.T) {
	// error-retry: 429, times: 2 — first two calls fail, third succeeds.
	mock := tokenless.StartMock(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"flaky backend"}]}`

	// First call: 429
	resp, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 429, resp.StatusCode)

	// Second call: 429
	resp, err = http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 429, resp.StatusCode)

	// Third call: succeeds
	resp, err = http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "error-retry", resp.Header.Get("X-Tokenless-Scenario"))
}

func TestFailureInjectionHard(t *testing.T) {
	// error-hard: 500, times: -1 — always fails.
	mock := tokenless.StartMock(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"always fails"}]}`

	for range 3 {
		resp, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 500, resp.StatusCode)
	}
}

func TestFailureInjectionMalformedSSE(t *testing.T) {
	// malformed-sse: emits non-JSON data: frame early in the stream.
	mock := tokenless.StartMock(t)
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"garbled stream"}]}`

	resp, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	scanner := bufio.NewScanner(resp.Body)
	var sawMalformed bool
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		if payload == "{this is not json" {
			sawMalformed = true
		}
	}
	require.True(t, sawMalformed, "expected malformed SSE frame")
}

func TestFailureInjectionStalledStream(t *testing.T) {
	// stalled-stream: stall: { times: 1 } — first stream stalls after role delta.
	mock := tokenless.StartMock(t)
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"stall the stream"}]}`

	// First call: stalls (we just check it starts streaming then hangs)
	resp, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Read initial data frame then close
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	require.Greater(t, n, 0, "expected at least the role delta before stall")

	// Second call: succeeds (stall consumed)
	resp2, err := http.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, 200, resp2.StatusCode)

	scanner := bufio.NewScanner(resp2.Body)
	var content string
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		if strings.Contains(payload, `"content"`) {
			content = "found"
		}
	}
	require.Equal(t, "found", content, "expected content after stall consumed")
}
