package harness

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	require "github.com/stretchr/testify/require"
)

// CapturePane returns the last 200 lines of the tmux session's pane, or ""
// if it cannot be read.
func CapturePane(session string) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", session, "-p", "-S", "-200").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// SendKeys sends one send-keys call to the session (literal text needs a
// leading "-l" arg; named keys like Enter are passed bare).
func SendKeys(t *testing.T, session string, args ...string) {
	t.Helper()
	full := append([]string{"send-keys", "-t", session}, args...)
	require.NoError(t, exec.Command("tmux", full...).Run())
}

// WaitForPane polls the pane until it contains want or timeout elapses.
func WaitForPane(t *testing.T, session, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(CapturePane(session), want) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
