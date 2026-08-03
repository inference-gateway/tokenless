package gateway_test

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

// Example shows the smallest possible setup: serve the built-in scenario
// library and point your app's gateway URL at it.
func Example() {
	ts := httptest.NewServer(gateway.New())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)

	fmt.Println(resp.StatusCode, len(b) > 0)
	// Output: 200 true
}

// ExampleLoad shows an inline scenario with a scripted tool call: turn 0
// asks for a Write, turn 1 (after the app sends the tool result back)
// closes the conversation.
func ExampleLoad() {
	defs, err := gateway.Load([]byte(`
fallback:
  content: "Done."
scenarios:
  - name: write-file
    match: '(?i)create a file'
    turns:
      - tool_calls:
          - { name: Write, args: { file_path: "hi.txt", content: "hi" } }
      - content: "The file was written."
`))
	if err != nil {
		panic(err)
	}

	ts := httptest.NewServer(gateway.New(defs))
	defer ts.Close()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"create a file"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()

	fmt.Println(resp.Header.Get("X-Tokenless-Scenario"), resp.Header.Get("X-Tokenless-Step"))
	// Output: write-file 0
}

// ExampleLoadFile shows the app-owned scenarios mode: an app repo ships its
// own scenarios.yaml and loads it instead of the built-in library. The same
// file works with the standalone binary (`tokenless --scenarios` or
// TOKENLESS_SCENARIOS) without writing any Go.
func ExampleLoadFile() {
	defs, err := gateway.LoadFile("../examples/scenarios.yaml")
	if err != nil {
		panic(err)
	}

	fmt.Println(len(defs.Scenarios) > 0)
	// Output: true
}

// ExampleServer_ServeHTTP_streaming shows the SSE path: the same scenario
// turn is streamed as chat.completion.chunk frames when the request asks for
// stream, ending with the [DONE] sentinel.
func ExampleServer_ServeHTTP_streaming() {
	ts := httptest.NewServer(gateway.New())
	defer ts.Close()

	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"say hello"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames, done := 0, false
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			done = true
			continue
		}
		frames++
	}

	fmt.Println(resp.Header.Get("Content-Type"), frames > 0, done)
	// Output: text/event-stream true true
}

// ExampleServer_Requests shows request recording: after driving the app,
// assert on exactly what it sent - endpoint, matched scenario, and (via
// Body or RawBody) any field of the original request JSON.
func ExampleServer_Requests() {
	srv := gateway.New()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"say hello"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()

	rec := srv.Requests()[0]
	fmt.Println(rec.Endpoint, rec.Scenario, rec.Body.Messages[0].Content.Text())
	// Output: /v1/chat/completions text-only say hello
}
