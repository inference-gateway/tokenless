// Package harness provides reusable helpers for hermetic end-to-end tests of
// agent apps: building a binary once per test run, running it as a subprocess
// against an in-test mock gateway (gateway package), and asserting on
// newline-delimited JSON output.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	require "github.com/stretchr/testify/require"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

// BuildBinary builds the Go main package at srcDir into a temp dir and
// returns the binary path plus a cleanup func. Intended for TestMain: build
// once, share the path across tests. Honors reuseEnvVar (e.g.
// "MYAPP_E2E_BINARY"): when that environment variable is set, its value is
// returned as-is and no build happens.
func BuildBinary(srcDir, reuseEnvVar string) (string, func(), error) {
	if reuseEnvVar != "" {
		if p := os.Getenv(reuseEnvVar); p != "" {
			return p, func() {}, nil
		}
	}

	dir, err := os.MkdirTemp("", "e2e-bin-*")
	if err != nil {
		return "", nil, err
	}
	binPath := filepath.Join(dir, "app")

	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = srcDir
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("building %s: %w\n%s", srcDir, err, out)
	}
	return binPath, func() { _ = os.RemoveAll(dir) }, nil
}

// StartMock serves a scenario library on an httptest server tied to the
// test's lifetime and returns the server plus its URL. With no arguments the
// built-in library is served.
func StartMock(t *testing.T, defs ...*gateway.ScenarioFile) (*gateway.Server, string) {
	t.Helper()
	gw := gateway.New(defs...)
	ts := httptest.NewServer(gw)
	t.Cleanup(ts.Close)
	return gw, ts.URL
}

// App describes the binary under test. Zero values work: Dir defaults to a
// fresh temp dir, Timeout to 30s. Env entries (typically at least the app's
// gateway-URL variable pointed at the mock) are appended on top of a hermetic
// base whose HOME is a temp dir, so the user's real config never leaks in.
type App struct {
	Bin     string
	Dir     string
	Env     map[string]string
	Stdin   string
	Timeout time.Duration
}

// Result is one finished App run.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes the app with the given arguments and waits for it to exit.
func (a App) Run(t *testing.T, args ...string) Result {
	t.Helper()
	timeout := a.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	dir := a.Dir
	if dir == "" {
		dir = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.Bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	for k, v := range a.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if a.Stdin != "" {
		cmd.Stdin = strings.NewReader(a.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	require.NoError(t, ctx.Err(), "run exceeded the test timeout; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "unexpected non-exit error: %v; stderr:\n%s", err, stderr.String())
		exitCode = exitErr.ExitCode()
	}
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}
}

// JSONLines parses every non-empty stdout line into a generic map; headless
// agents emit newline-delimited JSON only.
func JSONLines(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &obj), "non-JSON stdout line: %q", line)
		lines = append(lines, obj)
	}
	return lines
}

// ContentsByRole returns the content of every line whose "role" matches.
func ContentsByRole(lines []map[string]any, role string) []string {
	var out []string
	for _, l := range lines {
		if l["role"] == role {
			content, _ := l["content"].(string)
			out = append(out, content)
		}
	}
	return out
}

// StatusOfType returns the first line whose "type" matches, or nil.
func StatusOfType(lines []map[string]any, typ string) map[string]any {
	for _, l := range lines {
		if l["type"] == typ {
			return l
		}
	}
	return nil
}

// WriteFixtures creates one small fixture file per name inside dir.
func WriteFixtures(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("fixture content\n"), 0o600))
	}
}

// ToolMessages returns the tool-role messages of a chat completion request.
func ToolMessages(body gateway.CreateChatCompletionRequest) []gateway.Message {
	var out []gateway.Message
	for _, m := range body.Messages {
		if m.Role == gateway.Tool {
			out = append(out, m)
		}
	}
	return out
}
