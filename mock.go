// Package tokenless provides reusable helpers for hermetic end-to-end tests of
// agent apps: building a binary once per test run, running it as a subprocess
// against an in-test mock gateway, and asserting on newline-delimited JSON
// output and recorded expectation failures.
package tokenless

import (
	"crypto/x509"
	"net/http/httptest"
	"testing"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

// TestingT is the minimal interface for AssertExpectations, mirroring
// testify/mock's pattern so the library does not import "testing".
type TestingT interface {
	Errorf(format string, args ...interface{})
	Helper()
}

// Mock wraps the gateway server with test-friendly assertion methods.
type Mock struct {
	*gateway.Server
	// URL is the base URL of the running mock server (https:// when started
	// with StartMockTLS).
	URL string
	// CA is the certificate authority the mock's TLS cert is signed with;
	// nil for a plain-HTTP mock. Add it to a client x509.CertPool in tests.
	CA *x509.Certificate
}

// StartMock serves a scenario library on an httptest server tied to the
// test's lifetime and returns the Mock. With no arguments the built-in
// library is served.
func StartMock(t *testing.T, defs ...*gateway.ScenarioFile) *Mock {
	t.Helper()
	gw := gateway.New(defs...)
	ts := httptest.NewServer(gw)
	t.Cleanup(ts.Close)
	return &Mock{Server: gw, URL: ts.URL}
}

// StartMockTLS serves a scenario library over TLS on an httptest TLS server
// tied to the test's lifetime. The generated server certificate is
// self-signed, so Mock.CA is the CA clients must trust: build an HTTP
// client with Transport.TLSClientConfig.RootCAs containing it. Use
// httptest.NewTLSServer in-process instead of the binary when tests need a
// TLS upstream without generating their own cert files.
func StartMockTLS(t *testing.T, defs ...*gateway.ScenarioFile) *Mock {
	t.Helper()
	gw := gateway.New(defs...)
	ts := httptest.NewTLSServer(gw)
	t.Cleanup(ts.Close)
	return &Mock{Server: gw, URL: ts.URL, CA: ts.Certificate()}
}

// AssertExpectations fails the test if any expectation mismatches were
// recorded, printing a readable want/got diff for each failure.
func (m *Mock) AssertExpectations(t TestingT) {
	t.Helper()
	for _, f := range m.ExpectFailures() {
		t.Errorf("expectation mismatch: scenario=%q step=%d field=%s\n  want: %s\n  got:  %s",
			f.Scenario, f.Step, f.Field, f.Want, f.Got)
	}
}
