package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var (
	cats = []Option{{ID: 1, Name: "Bürobedarf", Direction: "outgoing"}, {ID: 2, Name: "Erlöse", Direction: "income"},
		{ID: 3, Name: "Telefon & Internet", Direction: "outgoing"}}
	cons = []Option{{ID: 7, Name: "T-Mobile Austria"}, {ID: 8, Name: "Alpenblick Hotel GmbH"}}
	line = Line{BookedOn: "2026-09-03", AmountCents: -4990, Counterparty: "T-Mobile Austria GmbH",
		Purpose: "Rechnung 09/2026 Mobilfunk"}
)

// fake answers with content and records what it was asked.
func fake(t *testing.T, content string, status int, seen *map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if seen != nil {
			json.Unmarshal(body, seen)
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": content}})
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-model")
}

func TestProposalIsValidatedAgainstTheRealData(t *testing.T) {
	var seen map[string]any
	c := fake(t, `{"description":"Mobilfunk September","category_id":3,"contact_id":7,"vat_rate":"20","confidence":"high"}`, 200, &seen)
	p, err := c.Propose(context.Background(), line, cats, cons)
	if err != nil {
		t.Fatal(err)
	}
	if p.Description != "Mobilfunk September" || p.CategoryID == nil || *p.CategoryID != 3 ||
		p.ContactID == nil || *p.ContactID != 7 || p.VATRate == nil || *p.VATRate != 20 {
		t.Errorf("proposal = %+v", p)
	}

	// The request: schema-constrained, deterministic, and only the
	// categories that fit the direction.
	if seen["format"] == nil || seen["stream"] != false {
		t.Errorf("request lacks the schema or streams: %v", seen)
	}
	if opts, _ := seen["options"].(map[string]any); opts["temperature"] != float64(0) {
		t.Errorf("temperature = %v, want 0", opts["temperature"])
	}
	msgs, _ := json.Marshal(seen["messages"])
	if !strings.Contains(string(msgs), "T-Mobile Austria GmbH") || strings.Contains(string(msgs), "Erlöse") {
		t.Errorf("prompt = %s", msgs)
	}
}

func TestInventedOrUnfitAnswersAreDropped(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		check   func(Proposal) bool
	}{
		"unknown ids and rate": {`{"description":"x","category_id":99,"contact_id":42,"vat_rate":"7","confidence":"high"}`,
			func(p Proposal) bool { return p.CategoryID == nil && p.ContactID == nil && p.VATRate == nil }},
		"contact not named in the bank line": {`{"description":"x","category_id":3,"contact_id":8,"vat_rate":"unknown","confidence":"high"}`,
			func(p Proposal) bool { return p.ContactID == nil && p.CategoryID != nil }},
		"income category for an expense": {`{"description":"x","category_id":2,"contact_id":0,"vat_rate":"unknown","confidence":"high"}`,
			func(p Proposal) bool { return p.CategoryID == nil && p.VATRate == nil }},
		"low confidence is flagged, not dropped": {`{"description":"vielleicht Telefon","category_id":3,"contact_id":7,"vat_rate":"20","confidence":"low"}`,
			func(p Proposal) bool {
				return p.Unsure && p.CategoryID != nil && *p.CategoryID == 3 && p.ContactID != nil
			}},
		"control characters and length": {`{"description":"a\nb\u0007` + strings.Repeat("x", 300) + `","category_id":0,"contact_id":0,"vat_rate":"unknown","confidence":"medium"}`,
			func(p Proposal) bool {
				return !strings.ContainsAny(p.Description, "\n\a") && len([]rune(p.Description)) <= 120
			}},
	} {
		p, err := fake(t, tc.content, 200, nil).Propose(context.Background(), line, cats, cons)
		if err != nil || !tc.check(p) {
			t.Errorf("%s: %+v, %v", name, p, err)
		}
	}
}

func TestFailuresAreErrorsNotProposals(t *testing.T) {
	if _, err := fake(t, "Gerne! Hier ist mein Vorschlag …", 200, nil).Propose(context.Background(), line, cats, cons); err == nil {
		t.Error("a non-JSON answer was accepted")
	}
	if _, err := fake(t, "", 500, nil).Propose(context.Background(), line, cats, cons); err == nil {
		t.Error("a server error was accepted")
	}
	if _, err := New("http://127.0.0.1:1", "").Propose(context.Background(), line, cats, cons); err == nil {
		t.Error("an unreachable server was accepted")
	}
}

// Against a real model, when one is available: TEST_LLM_URL=http://127.0.0.1:11434.
// Small models vary, so this checks only what must hold for any answer: it
// parses, and whatever survives validation is real data.
func TestRealModel(t *testing.T) {
	url := os.Getenv("TEST_LLM_URL")
	if url == "" {
		t.Skip("TEST_LLM_URL not set")
	}
	c := New(url, os.Getenv("TEST_LLM_MODEL"))
	p, err := c.Propose(context.Background(), line, cats, cons)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("proposal from %s: %q category=%v contact=%v vat=%v", c.Model, p.Description,
		deref(p.CategoryID), deref(p.ContactID), deref(p.VATRate))
	if p.Description == "" {
		t.Error("the model gave no description")
	}
	// The telephone line has one fitting category; a model that picks
	// another one is not usable for this.
	if p.CategoryID != nil && *p.CategoryID != 3 {
		t.Errorf("category %d for a mobile phone bill", *p.CategoryID)
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
