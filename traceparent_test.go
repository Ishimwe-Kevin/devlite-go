package devlite

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Node SDK injects a W3C traceparent header on outbound calls; a Go
// service receiving it must continue the SAME trace (traceId, parentId,
// sampled) instead of starting an orphaned one.
func TestMiddlewareContinuesIncomingTraceparent(t *testing.T) {
	incoming := "00-0123456789abcdef0123456789abcdef-0011223344556677-01"

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
	cfg.SampleRate = 1.0

	c := newClient(cfg)
	defer c.Close()

	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest("GET", "/hello", nil)
	req.Header.Set("traceparent", incoming)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	c.Flush()

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", rec.Code)
	}

	var ev map[string]any
	for _, e := range got {
		if e["type"] == "request" {
			ev = e
			break
		}
	}
	if ev == nil {
		t.Fatalf("no request event captured (got %v)", got)
	}
	if ev["traceId"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("traceId = %v, want caller's 32-hex trace id", ev["traceId"])
	}
	if ev["parentId"] != "0011223344556677" {
		t.Errorf("parentId = %v, want caller's span id", ev["parentId"])
	}
	if ev["id"] != ev["spanId"] {
		t.Errorf("root-span id (%v) != spanId (%v)", ev["id"], ev["spanId"])
	}
}

func TestParseTraceparentRejectsMalformed(t *testing.T) {
	bad := []string{
		"", "00-", "00-short-0011223344556677-01",
		"00-0123456789abcdef0123456789abcdef-short-01",
		"00-0123456789abcdef0123456789abcdef-0011223344556677-zz",
	}
	for _, h := range bad {
		if traceId, parentId, sampled, ok := ParseTraceparent(h); ok {
			t.Errorf("ParseTraceparent(%q) accepted, want reject (got %s %s %v)", h, traceId, parentId, sampled)
		}
	}
}

func TestParseTraceparentSampledFlag(t *testing.T) {
	_, _, sampled, ok := ParseTraceparent("00-0123456789abcdef0123456789abcdef-0011223344556677-01")
	if !ok || !sampled {
		t.Errorf("flags 01 should be sampled")
	}
	_, _, sampled, ok = ParseTraceparent("00-0123456789abcdef0123456789abcdef-0011223344556677-00")
	if !ok || sampled {
		t.Errorf("flags 00 should not be sampled")
	}
}

func TestScopeTraceparentEmitsW3CHeader(t *testing.T) {
	s := newScope()
	s.SetTraceId("0123456789abcdef0123456789abcdef")
	header := s.Traceparent()
	if !strings.HasPrefix(header, "00-0123456789abcdef0123456789abcdef-") {
		t.Errorf("header = %q, want caller trace continued", header)
	}
	parts := strings.Split(header, "-")
	if len(parts) != 4 || len(parts[2]) != 16 || parts[3] != "01" {
		t.Errorf("header = %q, not W3C-shaped", header)
	}

	// Local 16-hex traces stay internal — no W3C header.
	local := newScope()
	local.SetTraceId("0123456789abcdef")
	if got := local.Traceparent(); got != "" {
		t.Errorf("Traceparent() with 16-hex trace = %q, want \"\"", got)
	}
}