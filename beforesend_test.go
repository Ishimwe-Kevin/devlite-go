package devlite

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBeforeSendDropsAndMutatesEvents(t *testing.T) {
	var got []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBatch(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := defaultConfig()
	cfg.APIKey = "dl_test_key"
	cfg.Endpoint = server.URL + "/v1/events"
	cfg.Gzip = false
	cfg.FlushIntervalMs = 0
	cfg.MaxRetries = 1
	cfg.BeforeSend = func(e map[string]any) map[string]any {
		if e["message"] == "drop me" {
			return nil
		}
		e["extraEnriched"] = true
		return e
	}

	tr := newTransport(cfg)
	defer tr.Close()
	tr.Enqueue(map[string]any{"type": "log", "message": "drop me"})
	tr.Enqueue(map[string]any{"type": "log", "message": "keep me"})
	tr.Flush()

	if len(got) != 1 {
		t.Fatalf("want 1 delivered event, got %d: %v", len(got), got)
	}
	e := got[0]
	if e["message"] != "keep me" {
		t.Fatalf("wrong event delivered: %v", e)
	}
	if e["extraEnriched"] != true {
		t.Fatalf("beforeSend mutation not applied: %v", e)
	}
}

func TestBeforeSendPanicDropsEventWithoutCrashing(t *testing.T) {
	var got int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	panicThrowing := func(e map[string]any) map[string]any { panic("hook exploded") }

	cfg := defaultConfig()
	cfg.APIKey = "dl_test_key"
	cfg.Endpoint = server.URL + "/v1/events"
	cfg.Gzip = false
	cfg.FlushIntervalMs = 0
	cfg.MaxRetries = 1
	cfg.BeforeSend = panicThrowing

	tr := newTransport(cfg)
	defer tr.Close()
	tr.Enqueue(map[string]any{"type": "metric", "name": "x", "value": 1})
	tr.Flush()

	if got != 0 {
		t.Fatalf("throwing beforeSend must drop the event, but %d batches were sent", got)
	}
}

// decodeBatch reads a JSON request body into []map[string]any.
func decodeBatch(t *testing.T, r *http.Request) []map[string]any {
	t.Helper()
	var payload struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	return payload.Events
}