package devlite

import (
	"bufio"
	"net"
	"net/http"
	"sync"
)

// Automatic net/http instrumentation. Because Go's web frameworks all build
// on net/http, this one middleware covers stdlib, chi, Echo (via
// echo.WrapMiddleware), and Gin (wrap the router or see the README).

var (
	sampleMu    sync.Mutex
	sampleAccum float64
)

// sampleDecision returns the coherent-sampling decision for a request.
//
// Uses a Bresenham-style running accumulator instead of a counter+modulo
// scheme: every call adds `rate` to a shared accumulator and fires "keep"
// whenever the accumulator crosses 1.0, subtracting 1.0 afterward. This
// keeps almost exactly N*rate requests for any rate in (0,1), evenly
// spaced. (The old `every := int64(1.0/rate); n%every == 0` collapsed for
// any rate > 0.5 — 1.0/rate truncates to 1, so every request was kept.)
func sampleDecision(rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	sampleMu.Lock()
	sampleAccum += rate
	keep := sampleAccum >= 1.0
	if keep {
		sampleAccum -= 1.0
	}
	sampleMu.Unlock()
	return keep
}

// statusRecorder captures the response status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Flush passes through http.Flusher support (streaming responses).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack passes through http.Hijacker support (websockets).
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Middleware wraps an http.Handler with DevLite request/error tracking.
// Works directly with stdlib and chi: chi.Use(devlite.Middleware(...)).
func (c *Client) Middleware(next http.Handler) http.Handler {
	if c == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.ServeHTTPScope(w, r, func(rw http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(rw, req)
		})
	})
}

// ServeHTTPScope instruments a single request. `next` receives the wrapped
// ResponseWriter (so the status code can be captured) and the request whose
// context carries the request scope. Works for custom integrations
// (Gin/Echo): call it inside your middleware.
func (c *Client) ServeHTTPScope(w http.ResponseWriter, r *http.Request, next func(rw http.ResponseWriter, r *http.Request)) {
	scope := newScope()
	scope.SetTraceId(newHex(8))
	setAmbient(scope)
	defer setAmbient(nil)

	// Thread the scope through the request context so spawned goroutines and
	// Ctx-variants (SetUserCtx, CaptureErrorCtx) keep tagging this request.
	r = r.WithContext(WithScope(r.Context(), scope))

	scope.SetSampled(sampleDecision(c.cfg.SampleRate))

	start := nowMs()
	rw := &statusRecorder{ResponseWriter: w}

	panicked := false
	func() {
		defer func() {
			if p := recover(); p != nil {
				panicked = true
				c.enqueue(scope, buildPanicEvent(c.cfg, p, scope))
				panic(p) // never swallow errors
			}
		}()
		next(rw, r)
	}()

	status := rw.status
	if status == 0 {
		status = http.StatusOK
	}
	if panicked {
		status = http.StatusInternalServerError
	}

	durationMs := nowMs() - start
	method := r.Method
	path := r.URL.Path

	scope.AddBreadcrumb(map[string]any{
		"type": "request", "method": method, "path": path,
		"statusCode": status, "durationMs": durationMs, "traceId": scope.TraceId(),
	})

	var headers map[string]any
	if c.cfg.CaptureBody {
		headers = c.captureHeaders(r)
	}

	c.enqueue(scope, map[string]any{
		"type": "request", "method": method, "path": path,
		"statusCode": status, "durationMs": durationMs,
		"headers": headers, "timestamp": start,
	})
	if durationMs > slowRequestThresholdMs {
		c.enqueue(scope, map[string]any{
			"type": "slow_request", "method": method, "path": path,
			"durationMs": durationMs, "timestamp": start,
		})
	}
}

func (c *Client) captureHeaders(r *http.Request) map[string]any {
	redact := map[string]bool{}
	for _, h := range c.cfg.RedactHeaders {
		redact[lower(h)] = true
	}
	out := map[string]any{}
	for k, v := range r.Header {
		if redact[lower(k)] {
			out[k] = "[REDACTED]"
		} else if len(v) == 1 {
			out[k] = v[0]
		} else {
			out[k] = v
		}
	}
	return c.scrubIfEnabled(out).(map[string]any)
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// package-level helpers for the net/http story ------------------------------

// Middleware wraps next with the default client's tracking middleware.
func Middleware(next http.Handler) http.Handler {
	c := DefaultClient()
	if c == nil {
		return next
	}
	return c.Middleware(next)
}

// ServeHTTPScope runs a request through the default client's scope.
func ServeHTTPScope(w http.ResponseWriter, r *http.Request, next func(rw http.ResponseWriter)) {
	c := DefaultClient()
	if c == nil {
		next(w)
		return
	}
	c.ServeHTTPScope(w, r, func(rw http.ResponseWriter, _ *http.Request) { next(rw) })
}
