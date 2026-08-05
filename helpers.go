package tokenless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	require "github.com/stretchr/testify/require"

	gateway "github.com/inference-gateway/tokenless/gateway"
)

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
