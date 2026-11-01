package tokenless

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"strings"
	"testing"

	require "github.com/stretchr/testify/require"
)

// TestStartMockTLS proves a TLS mock serves HTTPS a Go client can trust via
// the exposed CA without disabling verification.
func TestStartMockTLS(t *testing.T) {
	mock := StartMockTLS(t)
	require.NotNil(t, mock.CA, "StartMockTLS must expose the server CA")
	require.True(t, strings.HasPrefix(mock.URL, "https://"), "URL = %q", mock.URL)

	pool := x509.NewCertPool()
	pool.AddCert(mock.CA)
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"say hello"}]}`
	resp, err := hc.Post(mock.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(b), "Hello! How can I help?")
}
