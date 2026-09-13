package devlite

import (
	"bytes"
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Scope is the request-scoped state: breadcrumbs, tags, the current user,
// the coherent-sampling decision, and the request's traceId. HTTP middleware
// creates one per request; the manual API calls it "the current request".
type Scope struct {
	mu          sync.RWMutex
	breadcrumbs []map[string]any
	tags        map[string]any
	user        map[string]any
	sampled     *bool
	traceId     string
	parentId    string
}

func newScope() *Scope { return &Scope{} }

// AddBreadcrumb records a rolling breadcrumb (max 20), mirroring the other
// SDKs. Recent breadcrumbs attach to the next captured error.
func (s *Scope) AddBreadcrumb(entry map[string]any) {
	if s == nil {
		return
	}
	e := make(map[string]any, len(entry)+1)
	for k, v := range entry {
		e[k] = v
	}
	e["timestamp"] = nowMs()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breadcrumbs = append(s.breadcrumbs, e)
	if len(s.breadcrumbs) > maxBreadcrumbs {
		s.breadcrumbs = s.breadcrumbs[len(s.breadcrumbs)-maxBreadcrumbs:]
	}
}

// SetUser scopes a user to the current request.
func (s *Scope) SetUser(user map[string]any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = user
}

// SetTag tags all events captured in this scope.
func (s *Scope) SetTag(key string, value any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = map[string]any{}
	}
	s.tags[key] = value
}

// SetSampled records the coherent-sampling decision for this request.
func (s *Scope) SetSampled(v bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sampled = &v
	s.mu.Unlock()
}

// SampledOut reports whether the request was sampled out.
func (s *Scope) SampledOut() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sampled != nil && !*s.sampled
}

// SetTraceId scopes the request's traceId so captured events link to it.
func (s *Scope) SetTraceId(traceId string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.traceId = traceId
	s.mu.Unlock()
}

// TraceId returns the request's traceId, or "" outside a request.
func (s *Scope) TraceId() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.traceId
}

// SetParentID records the incoming span this request continues (from a W3C
// traceparent header).
func (s *Scope) SetParentID(parentId string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.parentId = parentId
	s.mu.Unlock()
}

// ParentID returns the incoming parent span id, or "" for root requests.
func (s *Scope) ParentID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.parentId
}

func (s *Scope) snapshot() (breadcrumbs []map[string]any, user map[string]any, tags map[string]any, traceId string) {
	if s == nil {
		return nil, nil, nil, ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	breadcrumbs = make([]map[string]any, len(s.breadcrumbs))
	for i, b := range s.breadcrumbs {
		cp := make(map[string]any, len(b))
		for k, v := range b {
			cp[k] = v
		}
		breadcrumbs[i] = cp
	}
	if s.user != nil {
		user = make(map[string]any, len(s.user))
		for k, v := range s.user {
			user[k] = v
		}
	}
	if s.tags != nil {
		tags = make(map[string]any, len(s.tags))
		for k, v := range s.tags {
			tags[k] = v
		}
	}
	return breadcrumbs, user, tags, s.traceId
}

// --- goroutine-local ambient scope ----------------------------------------
//
// Go has no thread-locals. We keep a goroutine-id -> *Scope map (the same
// approach Sentry's Go SDK uses) so that CaptureError/SetUser/etc. work
// inside any request handler without threading a context through every
// call. The HTTP middleware seeds and cleans the current goroutine's scope.
// Handlers that spawn goroutines should pass the scope via context
// (WithScope / ScopeFromContext) or use the Ctx variants.

var (
	ambientMu sync.Mutex
	ambient   = map[int64]*Scope{}
)

func goid() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := buf[:n]
	const prefix = "goroutine "
	b = b[len(prefix):]
	if end := bytes.IndexByte(b, ' '); end > 0 {
		b = b[:end]
	}
	id, _ := strconv.ParseInt(string(b), 10, 64)
	return id
}

func setAmbient(s *Scope) {
	ambientMu.Lock()
	if s == nil {
		delete(ambient, goid())
	} else {
		ambient[goid()] = s
	}
	ambientMu.Unlock()
}

func ambientScope() *Scope {
	ambientMu.Lock()
	defer ambientMu.Unlock()
	return ambient[goid()]
}

// --- context integration ---------------------------------------------------

type scopeCtxKey struct{}

// WithScope attaches a scope to ctx so spawned goroutines can keep tagging
// events to the same request.
func WithScope(ctx context.Context, s *Scope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, scopeCtxKey{}, s)
}

// ScopeFromContext returns the scope attached with WithScope, or nil.
func ScopeFromContext(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	if s, ok := ctx.Value(scopeCtxKey{}).(*Scope); ok {
		return s
	}
	return nil
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}
