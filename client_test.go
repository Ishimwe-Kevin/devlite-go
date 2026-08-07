package devlite

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// ingestServer returns a httptest server that records the last received
// batch and the User-Agent / Content-Encoding headers, plus counters.
type ingestServer struct {
	ts         *httptest.Server
	batches    chan []map[string]any
	userAgents chan string
	encodings  chan string
	status     atomic.Int32
	failures   atomic.Int32
}

func newIngestServer(t *testing.T) *ingestServer {
	t.Helper()
	s := &ingestServer{
		batches:    make(chan []map[string]any, 100),
		userAgents: make(chan string, 100),
		encodings:  make(chan string, 100),
	}
	s.status.Store(200)
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.userAgents <- r.Header.Get("User-Agent")
		s.encodings <- r.Header.Get("Content-Encoding")

		body := r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "bad gzip", http.StatusBadRequest)
				return
			}
			body = zr
			defer zr.Close()
		}
		raw, _ := io.ReadAll(body)
		if testing.Verbose() {
			t.Logf("ingest raw: %s", raw)
		}

		var p struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.Unmarshal(raw, &p)
		s.batches <- p.Events

		if code := s.status.Load(); code != 200 {
			s.failures.Add(1)
			w.WriteHeader(int(code))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *ingestServer) drain() []map[string]any {
	var out []map[string]any
	for {
		select {
		case ev := <-s.batches:
			out = append(out, ev...)
		default:
			return out
		}
	}
}

func newTestClient(t *testing.T, s *ingestServer) *Client {
	t.Helper()
	c, err := NewClient(
		WithAPIKey("dl_test_123"),
		WithEndpoint(s.ts.URL),
		WithServiceName("test-svc"),
		WithFlushIntervalMs(10),
		WithMaxBatchSize(100),
		WithMaxRetries(2),
		WithRetryBaseDelayMs(10),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestCaptureError(t *testing.T) {
	s := newIngestServer(t)
	c := newTestClient(t, s)

	c.CaptureError(errBoom(), map[string]any{"route": "/api/x"})
	c.Flush()

	events := s.drain()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev["type"] != "error" {
		t.Fatalf("type = %v", ev["type"])
	}
	if !strings.Contains(ev["message"].(string), "boom") {
		t.Fatalf("message = %v", ev["message"])
	}
	if ev["fingerprint"] == "" || ev["fingerprint"] == nil {
		t.Fatalf("missing fingerprint: %v", ev)
	}
	if sc, ok := ev["sourceContext"]; ok && sc == nil {
		t.Fatalf("sourceContext unexpectedly nil")
	}
	if ev["user"] != nil {
		t.Fatalf("user should be nil, got %v", ev["user"])
	}
	if env := c.cfg.Environment; env != "development" {
		t.Fatalf("default environment = %v", env)
	}
	ua := <-s.userAgents
	if !strings.Contains(ua, "devlite-go/") {
		t.Fatalf("UA = %q", ua)
	}
	if enc := <-s.encodings; enc != "gzip" {
		t.Fatalf("encoding = %q", enc)
	}
}

func TestScrubbing(t *testing.T) {
	s := newIngestServer(t)
	c := newTestClient(t, s)

	c.CaptureError(errBoom(), map[string]any{
		"email": "john@example.com",
		"card":  "4111111111111111",
		"token": "dl_live_abcdef",
		"safe":  "hello",
	})
	c.Flush()

	events := s.drain()
	extra := events[0]["extra"].(map[string]any)
	if extra["email"] == "john@example.com" {
		t.Fatalf("email not scrubbed: %v", extra["email"])
	}
	if extra["safe"] != "hello" {
		t.Fatalf("safe value changed: %v", extra["safe"])
	}
}

func TestRequestEventsAndCoherentSampling(t *testing.T) {
	s := newIngestServer(t)
	c, _ := NewClient(
		WithAPIKey("dl_test_123"),
		WithEndpoint(s.ts.URL),
		WithSampleRate(1.0),
		WithFlushIntervalMs(10),
		WithMaxRetries(1),
	)
	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.CaptureErrorCtx(r.Context(), errBoom(), map[string]any{"during": "handler"})
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	c.Flush()

	events := s.drain()
	var sawRequest, sawError bool
	for _, ev := range events {
		switch ev["type"] {
		case "request":
			sawRequest = true
			if ev["statusCode"] != float64(418) {
				t.Fatalf("statusCode = %v", ev["statusCode"])
			}
			if ev["durationMs"].(float64) < 0 {
				t.Fatalf("bad durationMs")
			}
		case "error":
			sawError = true
			if ev["user"] != nil {
				t.Fatalf("no user set, got %v", ev["user"])
			}
		}
	}
	if !sawRequest || !sawError {
		t.Fatalf("want request+error, got %v", events)
	}

	// sampled out -> request AND error dropped together
	s2 := newIngestServer(t)
	c2, _ := NewClient(
		WithAPIKey("dl_test_123"),
		WithEndpoint(s2.ts.URL),
		WithSampleRate(0.0),
		WithFlushIntervalMs(10),
		WithMaxRetries(1),
	)
	h2 := c2.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c2.CaptureErrorCtx(r.Context(), errBoom(), nil)
		w.WriteHeader(http.StatusOK)
	}))
	h2.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	c2.Flush()
	if events := s2.drain(); len(events) != 0 {
		t.Fatalf("sampled-out request leaked %d events", len(events))
	}
}

func TestUserScope(t *testing.T) {
	s := newIngestServer(t)
	c := newTestClient(t, s)

	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.SetUserCtx(r.Context(), map[string]any{"id": "u-42", "email": "kevin@example.com"})
		c.CaptureErrorCtx(r.Context(), errBoom(), nil)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	c.Flush()

	events := s.drain()
	var errorEvent map[string]any
	for _, ev := range events {
		if ev["type"] == "error" {
			errorEvent = ev
		}
	}
	if errorEvent == nil {
		t.Fatal("no error event")
	}
	user := errorEvent["user"].(map[string]any)
	if user["id"] != "u-42" {
		t.Fatalf("user id = %v", user["id"])
	}
	if user["email"] == "kevin@example.com" {
		t.Fatalf("user email not scrubbed: %v", user["email"])
	}
}

func TestPanicCapture(t *testing.T) {
	s := newIngestServer(t)
	c := newTestClient(t, s)

	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))
	defer func() { _ = recover() }()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	c.Flush()

	events := s.drain()
	var panicEvent map[string]any
	for _, ev := range events {
		if ev["type"] == "error" && ev["fatal"] == true {
			panicEvent = ev
		}
	}
	if panicEvent == nil {
		t.Fatalf("no panic event captured: %v", events)
	}
	if panicEvent["message"] != "kaboom" {
		t.Fatalf("panic message = %v", panicEvent["message"])
	}
}

func TestRetryOnFailure(t *testing.T) {
	s := newIngestServer(t)
	s.status.Store(500)
	c := newTestClient(t, s)
	c.CaptureError(errBoom(), nil)
	c.Flush()
	if s.failures.Load() != 2 {
		t.Fatalf("want 2 failed attempts, got %d", s.failures.Load())
	}
}

func TestQueueBoundsAndDrain(t *testing.T) {
	q := newEventQueue(3)
	q.Push(map[string]any{"i": 1})
	q.Push(map[string]any{"i": 2})
	q.Push(map[string]any{"i": 3})
	q.Push(map[string]any{"i": 4})
	if q.Size() != 3 {
		t.Fatalf("size = %d", q.Size())
	}
	if q.Dropped() != 1 {
		t.Fatalf("dropped = %d", q.Dropped())
	}
	batch := q.Drain(2)
	if len(batch) != 2 || batch[0]["i"] != 2 || batch[1]["i"] != 3 {
		t.Fatalf("batch = %v", batch)
	}
	if q.Size() != 1 {
		t.Fatalf("remaining size = %d", q.Size())
	}
}

func TestContextScopePropagation(t *testing.T) {
	ctx := WithScope(context.Background(), newScope())
	if ScopeFromContext(ctx) == nil {
		t.Fatal("scope lost through context")
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := NewClient(WithSampleRate(2.0)); err == nil {
		t.Fatal("expected sample-rate validation error")
	}
	if _, err := NewClient(WithSampleRate(0.5)); err == nil {
		t.Fatal("expected API-key validation error")
	}
	if _, err := NewClient(WithAPIKey("dl_ok"), WithSampleRate(0.5)); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
