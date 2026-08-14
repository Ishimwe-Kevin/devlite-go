package devlite

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Span is a trace span you can End() once the work completes.
type Span struct {
	client     *Client
	scope      *Scope
	name       string
	ID         string
	TraceID    string
	ParentID   string
	Tags       map[string]any
	startTime  int64
	endTime    int64
	durationMs int64
	status     string
	ended      bool
}

func newHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))[:n*2]
	}
	return hex.EncodeToString(buf)
}

// StartSpan begins a new trace span bound to the ambient scope. Spans created
// inside a request inherit its traceId; standalone spans generate their own,
// so auto-captured requests and their spans share one.
func (c *Client) StartSpan(name string, tags map[string]any) *Span {
	sc := c.resolveScope(nil)
	traceID := sc.TraceId()
	if traceID == "" {
		traceID = newHex(8)
	}
	return &Span{
		client:    c,
		scope:     sc,
		name:      name,
		ID:        newHex(6),
		TraceID:   traceID,
		Tags:      tags,
		startTime: nowMs(),
	}
}

// End finishes the span ("ok" | "error") and reports it.
func (s *Span) End(status string) {
	if s == nil || s.ended || s.client == nil {
		return
	}
	s.ended = true
	s.endTime = nowMs()
	s.durationMs = s.endTime - s.startTime
	s.status = status
	event := s.client.scrubIfEnabled(map[string]any{
		"type":       "span",
		"id":         s.ID,
		"name":       s.name,
		"traceId":    s.TraceID,
		"parentId":   s.ParentID,
		"tags":       s.Tags,
		"startTime":  s.startTime,
		"endTime":    s.endTime,
		"durationMs": s.durationMs,
		"status":     status,
		"timestamp":  s.endTime,
	}).(map[string]any)
	s.client.enqueue(s.scope, event)
}

// StartSpan begins a new trace span on the default client.
func StartSpan(name string, tags map[string]any) *Span {
	return requireClient().StartSpan(name, tags)
}
