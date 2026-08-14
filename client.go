package devlite

import (
	"context"
	"sync"
	"sync/atomic"
)

// Client owns config, transport, and the default scope. Normally you use
// the package-level functions after calling Init(); NewClient lets you run
// several isolated clients (useful in tests).
type Client struct {
	cfg          Config
	transport    *Transport
	defaultScope *Scope
	closed       atomic.Bool
	closeOnce    sync.Once
}

func newClient(cfg Config) *Client {
	return &Client{
		cfg:          cfg,
		transport:    newTransport(cfg),
		defaultScope: newScope(),
	}
}

// NewClient builds an isolated client from options.
func NewClient(opts ...Option) (*Client, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return newClient(cfg), nil
}

// --- package-level singleton -----------------------------------------------

var (
	clientMu      sync.Mutex
	defaultClient *Client
)

// Init initializes the default SDK client. Safe defaults are applied; only
// the API key is required. Calling Init more than once is a no-op.
func Init(opts ...Option) error {
	clientMu.Lock()
	defer clientMu.Unlock()
	if defaultClient != nil {
		return nil // already initialized
	}
	c, err := NewClient(opts...)
	if err != nil {
		return err
	}
	defaultClient = c
	return nil
}

// DefaultClient returns the client created by Init, or nil.
func DefaultClient() *Client {
	clientMu.Lock()
	defer clientMu.Unlock()
	return defaultClient
}

func requireClient() *Client {
	c := DefaultClient()
	if c == nil {
		return nil
	}
	return c
}

// resolveScope picks the scope for an event: an explicit ctx scope wins,
// then the current goroutine's ambient request scope, then the default.
func (c *Client) resolveScope(ctx context.Context) *Scope {
	if s := ScopeFromContext(ctx); s != nil {
		return s
	}
	if s := ambientScope(); s != nil {
		return s
	}
	return c.defaultScope
}

// coherentTypes are dropped together when a request is sampled out.
var coherentTypes = map[string]bool{
	"request": true, "slow_request": true, "error": true,
	"message": true, "metric": true, "log": true, "span": true,
}

// enqueue coherently drops sampled-out requests' events and inherits the
// request's traceId on every event (deployments are not request-scoped).
func (c *Client) enqueue(scope *Scope, event map[string]any) {
	if t, ok := event["type"].(string); ok && coherentTypes[t] {
		if scope != nil && scope.SampledOut() {
			return
		}
	}
	if t, ok := event["type"].(string); !ok || t != "deployment" {
		if scope != nil && scope.TraceId() != "" {
			if _, has := event["traceId"]; !has {
				event["traceId"] = scope.TraceId()
			}
		}
	}
	c.transport.Enqueue(event)
}

func (c *Client) scrubIfEnabled(v any) any {
	if !c.cfg.ScrubSensitiveData {
		return v
	}
	return scrub(v)
}

// --- manual capture API (also exposed at package level) --------------------

// CaptureError captures a handled error, enriching it with the ambient
// request's user, breadcrumbs, and tags.
func (c *Client) CaptureError(err error, extra map[string]any) {
	c.CaptureErrorCtx(context.Background(), err, extra)
}

// CaptureErrorCtx is CaptureError for handlers that carry a scope through
// context (e.g. after spawning a goroutine).
func (c *Client) CaptureErrorCtx(ctx context.Context, err error, extra map[string]any) {
	if c == nil {
		return
	}
	scope := c.resolveScope(ctx)
	event := buildErrorEvent(c.cfg, err, scope, extra, "", false)
	c.enqueue(scope, event)
}

// CaptureMessage captures a non-error event.
func (c *Client) CaptureMessage(level, message string, extra map[string]any) {
	scope := c.resolveScope(context.Background())
	event := c.scrubIfEnabled(map[string]any{
		"type": "message", "level": level, "message": message,
		"extra": extra, "timestamp": nowMs(),
	}).(map[string]any)
	c.enqueue(scope, event)
}

// ReportMetric reports a metric data point (feeds forecasting/anomalies).
func (c *Client) ReportMetric(name string, value float64, unit string, tags map[string]string) {
	if name == "" {
		return
	}
	scope := c.resolveScope(context.Background())
	event := c.scrubIfEnabled(map[string]any{
		"type": "metric", "name": name, "value": value, "unit": unit,
		"tags": toAnyMap(tags), "timestamp": nowMs(),
	}).(map[string]any)
	c.enqueue(scope, event)
}

// CaptureLog captures a structured log line.
func (c *Client) CaptureLog(level, message string, fields map[string]any) {
	scope := c.resolveScope(context.Background())
	event := c.scrubIfEnabled(map[string]any{
		"type": "log", "level": level, "message": message,
		"fields": fields, "timestamp": nowMs(),
	}).(map[string]any)
	c.enqueue(scope, event)
}

// ReportDeployment tells DevLite about a deploy (powers before/after views).
func (c *Client) ReportDeployment(version, commitSHA, notes string) {
	scope := c.resolveScope(context.Background())
	event := map[string]any{
		"type": "deployment", "version": version, "commitSha": commitSHA,
		"notes": notes, "timestamp": nowMs(),
	}
	c.enqueue(scope, event)
}

// AddBreadcrumb records a breadcrumb on the ambient request scope.
func (c *Client) AddBreadcrumb(entry map[string]any) {
	c.resolveScope(context.Background()).AddBreadcrumb(entry)
}

// SetUser identifies the user for the CURRENT request (ambient scope).
func (c *Client) SetUser(user map[string]any) {
	c.resolveScope(context.Background()).SetUser(user)
}

// SetUserCtx sets the user on the scope carried in ctx.
func (c *Client) SetUserCtx(ctx context.Context, user map[string]any) {
	if s := ScopeFromContext(ctx); s != nil {
		s.SetUser(user)
	}
}

// SetTag tags all events captured in the ambient scope.
func (c *Client) SetTag(key string, value any) {
	c.resolveScope(context.Background()).SetTag(key, value)
}

// Flush force-sends whatever is queued (useful in serverless).
func (c *Client) Flush() {
	if c == nil {
		return
	}
	c.transport.Flush()
}

// Close cleanly stops the SDK, flushing remaining events.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.transport.Close()
	})
}

// --- package-level wrappers -------------------------------------------------

// CaptureError captures a handled error on the default client.
func CaptureError(err error, extra map[string]any) { requireClient().CaptureError(err, extra) }

// CaptureErrorCtx captures a handled error using a scope from ctx.
func CaptureErrorCtx(ctx context.Context, err error, extra map[string]any) {
	requireClient().CaptureErrorCtx(ctx, err, extra)
}

// CaptureMessage captures a non-error event.
func CaptureMessage(level, message string, extra map[string]any) {
	requireClient().CaptureMessage(level, message, extra)
}

// ReportMetric reports a metric data point.
func ReportMetric(name string, value float64, unit string, tags map[string]string) {
	requireClient().ReportMetric(name, value, unit, tags)
}

// CaptureLog captures a structured log line.
func CaptureLog(level, message string, fields map[string]any) {
	requireClient().CaptureLog(level, message, fields)
}

// ReportDeployment tells DevLite about a deploy.
func ReportDeployment(version, commitSHA, notes string) {
	requireClient().ReportDeployment(version, commitSHA, notes)
}

// AddBreadcrumb records a breadcrumb on the current request.
func AddBreadcrumb(entry map[string]any) { requireClient().AddBreadcrumb(entry) }

// SetUser identifies the user for the CURRENT request.
func SetUser(user map[string]any) { requireClient().SetUser(user) }

// SetTag tags all subsequent events.
func SetTag(key string, value any) { requireClient().SetTag(key, value) }

// Flush force-sends queued events.
func Flush() { requireClient().Flush() }

// Close cleanly stops the SDK.
func Close() {
	if c := DefaultClient(); c != nil {
		c.Close()
	}
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
