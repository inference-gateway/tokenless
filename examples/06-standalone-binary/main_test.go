// Building the tokenless binary and running it as a standalone server
// subprocess. This demonstrates the consumption mode that does not require
// writing Go — any HTTP client (curl, a non-Go test suite) can talk to it.
package main

import (
	"bufio"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
)

func TestStandaloneBinary(t *testing.T) {
	bin, cleanup, err := tokenless.BuildBinary("../../cmd/tokenless", "TOKENLESS_E2E_BINARY")
	require.NoError(t, err)
	defer cleanup()

	// HTTP client with a timeout so the test never hangs.
	hc := &http.Client{Timeout: 5 * time.Second}

	// Start the binary on a free port.
	cmd := exec.Command(bin, "--port", "0")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	defer cmd.Process.Kill()
	defer cmd.Wait()

	// Read the "tokenless listening on http://..." line.
	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line := scanner.Text()
			if addr, ok := strings.CutPrefix(line, "tokenless listening on http://"); ok {
				urlCh <- "http://" + addr
			}
		}
	}()

	var url string
	select {
	case url = <-urlCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for binary to start")
	}

	// Wait for the server to be ready.
	require.Eventually(t, func() bool {
		resp, err := hc.Get(url + "/v1/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	}, 5*time.Second, 100*time.Millisecond)

	// Hit /v1/models.
	resp, err := hc.Get(url + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(b), "mock/openai/gpt-4o")

	// Hit /v1/chat/completions.
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"say hello"}]}`
	resp2, err := hc.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, 200, resp2.StatusCode)
	b2, _ := io.ReadAll(resp2.Body)
	require.Contains(t, string(b2), "Hello! How can I help?")
}
