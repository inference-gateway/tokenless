package tokenless

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	require "github.com/stretchr/testify/require"
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
	if _, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return binPath, func() { _ = os.RemoveAll(dir) }, nil
}

// Orchestrator describes the binary under test. Zero values work: Dir defaults
// to a fresh temp dir, Timeout to 30s. Env entries (typically at least the
// app's gateway-URL variable pointed at the mock) are appended on top of a
// hermetic base whose HOME is a temp dir, so the user's real config never
// leaks in.
type Orchestrator struct {
	Bin     string
	Dir     string
	Env     map[string]string
	Stdin   string
	Timeout time.Duration
}

// Result is one finished Orchestrator run.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes the orchestrator with the given arguments and waits for it to
// exit.
func (a Orchestrator) Run(t *testing.T, args ...string) Result {
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
