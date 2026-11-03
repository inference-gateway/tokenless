package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

const decisionsPath = "/v1/decisions?provider=ollama_cloud"

func postDecision(t *testing.T, srv *Server, body string) (int, DecisionResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, decisionsPath, bytes.NewBufferString(body)))
	var resp DecisionResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec.Code, resp
}

func answerByName(resp DecisionResponse, name string) map[string]any {
	for _, a := range resp.Answers {
		if a["name"] == name {
			return a
		}
	}
	return nil
}

func TestDecisions_DefaultsPerQuestionKind(t *testing.T) {
	srv := New()
	code, resp := postDecision(t, srv, `{"model":"clef:27b","input":"Buy now!!!","questions":[
		{"type":"predicate","name":"is_spam","instructions":"spam?"},
		{"type":"choice","name":"topic","instructions":"topic?","choices":[{"value":"billing"},{"value":"tech"}]},
		{"type":"score","name":"tone","instructions":"tone?","levels":[{"label":"rude"},{"label":"kind"}]}]}`)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, resp.Answers, 3)
	require.Equal(t, defaultDecisionProbability, answerByName(resp, "is_spam")["probability"])
	require.Equal(t, "billing", answerByName(resp, "topic")["choice"])
	require.Equal(t, 0.0, answerByName(resp, "tone")["score"])
	require.Equal(t, decisionInputTokens, resp.Usage.InputTokens)

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	require.Equal(t, "/v1/decisions", reqs[0].Endpoint)
	require.Equal(t, "ollama_cloud", reqs[0].Provider)
	require.Equal(t, "clef:27b", reqs[0].Model)
	require.Len(t, reqs[0].DecisionsBody.Questions, 3)
}

func TestDecisions_RulesMatchEvidence(t *testing.T) {
	defs, err := Load([]byte(`
decisions:
  - match: 'rm -rf'
    probability: 0.05
    choice: destructive
    score: 2
`))
	require.NoError(t, err)
	srv := New(defs)

	questions := `"questions":[{"type":"predicate","name":"call-0","instructions":"safe?"},{"type":"choice","name":"risk","instructions":"class?","choices":[{"value":"benign"},{"value":"destructive"}]},{"type":"score","name":"severity","instructions":"how bad?","levels":[{"label":"low"},{"label":"mid"},{"label":"high"}]}]`
	_, matched := postDecision(t, srv, `{"model":"clef","input":[{"type":"message","content":"Bash: rm -rf /"}],`+questions+`}`)
	require.Equal(t, 0.05, answerByName(matched, "call-0")["probability"])
	require.Equal(t, "destructive", answerByName(matched, "risk")["choice"])
	require.Equal(t, 2.0, answerByName(matched, "severity")["score"])

	_, unmatched := postDecision(t, srv, `{"model":"clef","input":"Bash: go test ./...",`+questions+`}`)
	require.Equal(t, defaultDecisionProbability, answerByName(unmatched, "call-0")["probability"])
	require.Equal(t, "benign", answerByName(unmatched, "risk")["choice"])
}

func TestDecisions_RejectsBadRequests(t *testing.T) {
	srv := New()
	for _, body := range []string{`{nope`, `{"model":"clef","input":"x","questions":[]}`} {
		code, _ := postDecision(t, srv, body)
		require.Equal(t, http.StatusBadRequest, code, body)
	}
}

func TestDecisions_InvalidRuleFailsLoad(t *testing.T) {
	for _, yaml := range []string{"decisions:\n  - match: '('\n", "decisions:\n  - match: 'x'\n    probability: 2\n"} {
		_, err := Load([]byte(yaml))
		require.Error(t, err, yaml)
	}
}

func TestModels_ListsTheDecisionModel(t *testing.T) {
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Contains(t, rec.Body.String(), DecisionModel)
	require.Contains(t, rec.Body.String(), `"decisions"`)
}
