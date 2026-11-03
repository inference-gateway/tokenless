package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// Decision question kinds of the OpenAI-compatible Decisions API.
const (
	DecisionPredicate = "predicate"
	DecisionChoice    = "choice"
	DecisionScore     = "score"
)

// defaultDecisionProbability is the p(true) of a predicate no decision rule
// matches: high enough to approve under any sane threshold.
const defaultDecisionProbability = 0.95

// decisionInputTokens is the usage reported for every decision request.
const decisionInputTokens = 10

// DecisionRule scripts the answers of decision requests whose evidence
// matches. The first matching rule wins. A rule leaves a field unset to keep
// the default for that question kind.
type DecisionRule struct {
	// Match is a Go regular expression tested (unanchored) against the
	// request input rendered as text.
	Match string `yaml:"match"`
	// Probability answers predicate questions (default 0.95).
	Probability *float64 `yaml:"probability,omitempty"`
	// Choice answers choice questions (default: the first option).
	Choice string `yaml:"choice,omitempty"`
	// Score answers score questions (default 0, the lowest level).
	Score *float64 `yaml:"score,omitempty"`

	re *regexp.Regexp
}

// DecisionQuestion is one question of a decision request.
type DecisionQuestion struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Choices      []struct {
		Value       string `json:"value"`
		Description string `json:"description,omitempty"`
	} `json:"choices,omitempty"`
	Levels []struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	} `json:"levels,omitempty"`
}

// CreateDecisionRequest is the decoded POST /v1/decisions body. Input keeps
// its wire form (a string or an item list) so rules can match either.
type CreateDecisionRequest struct {
	Model     string             `json:"model"`
	Input     json.RawMessage    `json:"input"`
	Questions []DecisionQuestion `json:"questions"`
}

// DecisionResponse is the POST /v1/decisions body: one answer per question,
// keyed by name, plus usage.
type DecisionResponse struct {
	Answers []map[string]any `json:"answers"`
	Usage   DecisionUsage    `json:"usage"`
}

// DecisionUsage is the usage block of a decision response.
type DecisionUsage struct {
	InputTokens int `json:"input_tokens"`
}

// handleDecisions answers POST /v1/decisions with one answer per question:
// predicates get a probability, choices an option and scores a level, taken
// from the first matching `decisions:` rule or the defaults. The request is
// recorded like a completion so tests can assert the questions asked.
func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"reading request body"}`, http.StatusBadRequest)
		return
	}
	var req CreateDecisionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if len(req.Questions) == 0 {
		http.Error(w, `{"error":"questions must list at least one question"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.reqs = append(s.reqs, Recorded{
		Endpoint:      r.URL.Path,
		Provider:      r.URL.Query().Get("provider"),
		Model:         req.Model,
		DecisionsBody: &req,
		RawBody:       raw,
	})
	s.mu.Unlock()

	rule := s.defs.decisionRule(decisionEvidence(req.Input))
	resp := DecisionResponse{Answers: make([]map[string]any, 0, len(req.Questions)), Usage: DecisionUsage{InputTokens: decisionInputTokens}}
	for _, q := range req.Questions {
		resp.Answers = append(resp.Answers, answerQuestion(q, rule))
	}
	writeJSON(w, resp)
}

// decisionEvidence renders the input for rule matching: a string input as
// is, anything else as its JSON text.
func decisionEvidence(input json.RawMessage) string {
	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		return text
	}
	return string(input)
}

func (f *ScenarioFile) decisionRule(evidence string) *DecisionRule {
	if f == nil {
		return nil
	}
	for i := range f.Decisions {
		if f.Decisions[i].re.MatchString(evidence) {
			return &f.Decisions[i]
		}
	}
	return nil
}

func answerQuestion(q DecisionQuestion, rule *DecisionRule) map[string]any {
	answer := map[string]any{"name": q.Name, "type": q.Type}
	switch q.Type {
	case DecisionPredicate:
		answer["probability"] = pick(rule, func(r *DecisionRule) *float64 { return r.Probability }, defaultDecisionProbability)
	case DecisionChoice:
		choice := firstChoice(q)
		if rule != nil && rule.Choice != "" {
			choice = rule.Choice
		}
		answer["choice"] = choice
		answer["probabilities"] = map[string]float64{choice: 1}
		answer["confidence"] = 1.0
	case DecisionScore:
		answer["score"] = pick(rule, func(r *DecisionRule) *float64 { return r.Score }, 0)
		answer["confidence"] = 1.0
	default:
		answer["refusal"] = fmt.Sprintf("unknown question type %q", q.Type)
	}
	return answer
}

func pick(rule *DecisionRule, field func(*DecisionRule) *float64, def float64) float64 {
	if rule == nil || field(rule) == nil {
		return def
	}
	return *field(rule)
}

func firstChoice(q DecisionQuestion) string {
	if len(q.Choices) == 0 {
		return ""
	}
	return q.Choices[0].Value
}
