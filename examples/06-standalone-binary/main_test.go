// Building the tokenless binary and running it as a standalone server
// subprocess. This demonstrates the consumption mode that does not require
// writing Go — any HTTP client (curl, a non-Go test suite) can talk to it.
package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	require "github.com/stretchr/testify/require"

	tokenless "github.com/inference-gateway/tokenless"
)

// bin is the tokenless binary under test, built once in TestMain.
var bin string

func TestMain(m *testing.M) {
	var cleanup func()
	var err error
	bin, cleanup, err = tokenless.BuildBinary("../../cmd/tokenless", "TOKENLESS_E2E_BINARY")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// startBinary launches the tokenless binary with extra env and returns its
// base URL, parsed from the readiness line. The process is killed on test
// cleanup.
func startBinary(t *testing.T, env map[string]string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if url, ok := strings.CutPrefix(scanner.Text(), "tokenless listening on "); ok {
				urlCh <- url
			}
		}
	}()

	select {
	case url := <-urlCh:
		return url
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for binary to start")
		return ""
	}
}

// tlsClient returns an HTTP client trusting the self-signed server
// certificate, optionally presenting a client certificate for mTLS.
func tlsClient(t *testing.T, serverCA, clientCert, clientKey string) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(serverCA)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM), "parsing %s", serverCA)
	cfg := &tls.Config{RootCAs: pool}
	if clientCert != "" {
		kp, err := tls.LoadX509KeyPair(clientCert, clientKey)
		require.NoError(t, err)
		cfg.Certificates = []tls.Certificate{kp}
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
}

func chatCompletions(t *testing.T, hc *http.Client, url string) string {
	t.Helper()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"say hello"}]}`
	resp, err := hc.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestStandaloneBinary(t *testing.T) {
	hc := &http.Client{Timeout: 5 * time.Second}
	url := startBinary(t, nil, "--port", "0")

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
	require.Contains(t, chatCompletions(t, hc, url), "Hello! How can I help?")
}

// TestStandaloneBinaryTLS exercises --tls-cert/--tls-key and their
// TOKENLESS_TLS_CERT/TOKENLESS_TLS_KEY environment equivalents.
func TestStandaloneBinaryTLS(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, _, _, _ := writeCerts(t, dir)
	hc := tlsClient(t, serverCert, "", "")

	url := startBinary(t, nil, "--port", "0", "--tls-cert", serverCert, "--tls-key", serverKey)
	require.True(t, strings.HasPrefix(url, "https://"), "readiness line = %q", url)

	resp, err := hc.Get(url + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(b), "mock/openai/gpt-4o")

	require.Contains(t, chatCompletions(t, hc, url), "Hello! How can I help?")

	// Same serve over environment variables instead of flags.
	t.Run("env fallback", func(t *testing.T) {
		url := startBinary(t, map[string]string{
			"TOKENLESS_TLS_CERT": serverCert,
			"TOKENLESS_TLS_KEY":  serverKey,
		}, "--port", "0")
		require.True(t, strings.HasPrefix(url, "https://"), "readiness line = %q", url)
		resp, err := hc.Get(url + "/v1/health")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
	})
}

// TestStandaloneBinaryMTLS exercises --tls-client-ca: a client presenting a
// certificate signed by the CA gets through, a client without one fails the
// handshake.
func TestStandaloneBinaryMTLS(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, clientCA, clientCert, clientKey := writeCerts(t, dir)
	url := startBinary(t, nil, "--port", "0", "--tls-cert", serverCert, "--tls-key", serverKey, "--tls-client-ca", clientCA)

	t.Run("valid client certificate", func(t *testing.T) {
		hc := tlsClient(t, serverCert, clientCert, clientKey)
		require.Contains(t, chatCompletions(t, hc, url), "Hello! How can I help?")
	})

	t.Run("no client certificate", func(t *testing.T) {
		hc := tlsClient(t, serverCert, "", "")
		_, err := hc.Post(url+"/v1/models", "application/json", nil)
		require.Error(t, err, "server must refuse clients without a certificate")
		require.ErrorContains(t, err, "certificate required")
	})
}

// TestStandaloneBinaryTLSFlagValidation checks half-configured TLS flags
// fail at startup with a clear error instead of silently serving plain HTTP.
func TestStandaloneBinaryTLSFlagValidation(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, clientCA, _, _ := writeCerts(t, dir)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"only --tls-cert", []string{"--port", "0", "--tls-cert", serverCert}, "--tls-cert and --tls-key must be provided together"},
		{"only --tls-key", []string{"--port", "0", "--tls-key", serverKey}, "--tls-cert and --tls-key must be provided together"},
		{"client CA without cert pair", []string{"--port", "0", "--tls-client-ca", clientCA}, "--tls-client-ca requires --tls-cert and --tls-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tokenless.Orchestrator{Bin: bin}.Run(t, tt.args...)
			require.NotZero(t, res.ExitCode, "should refuse to start; stdout:\n%s", res.Stdout)
			require.Contains(t, res.Stderr, tt.want)
		})
	}
}
