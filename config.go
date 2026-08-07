// Package devlite is the official Go SDK for DevLite — AI-powered
// observability. It mirrors the @devlite/nodejs (npm) and devlite (PyPI)
// SDKs: add two lines, get automatic request tracking, error capture,
// error grouping, source context, user tracking, and slow-endpoint
// detection. Zero external dependencies.
package devlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultEndpoint is the production DevLite ingest API.
	DefaultEndpoint  = "https://devlite.andasy.dev/v1/events"
	defaultUserAgent = "devlite-go/0.1.0"

	maxBreadcrumbs         = 20
	slowRequestThresholdMs = 1000
)

// Config holds every knobs of the SDK. Fill the fields you care about and
// pass the rest to Option functions — Init() starts from safe defaults
// (scrubbing on, request bodies off, bounded queue).
type Config struct {
	APIKey               string
	Endpoint             string
	Environment          string
	ServiceName          string
	Release              string
	SampleRate           float64 // 0.0–1.0; sampling is coherent
	CaptureBody          bool
	RedactHeaders        []string
	FlushIntervalMs      int
	MaxBatchSize         int
	MaxQueueSize         int
	Gzip                 bool
	Debug                bool
	MaxRetries           int
	RetryBaseDelayMs     int
	RequestTimeoutMs     int
	CaptureSourceContext bool
	ScrubSensitiveData   bool
	UserAgent            string
	OnError              func(error)
}

func defaultConfig() Config {
	service := os.Getenv("DEVLITE_SERVICE_NAME")
	if service == "" {
		if wd, err := os.Getwd(); err == nil {
			service = filepath.Base(wd)
		}
	}
	if service == "" || service == "." || service == string(filepath.Separator) {
		service = "go-service"
	}
	environment := os.Getenv("DEVLITE_ENVIRONMENT")
	if environment == "" {
		environment = "development"
	}
	return Config{
		APIKey:               os.Getenv("DEVLITE_API_KEY"),
		Endpoint:             DefaultEndpoint,
		Environment:          environment,
		ServiceName:          service,
		Release:              os.Getenv("DEVLITE_RELEASE"),
		SampleRate:           1.0,
		RedactHeaders:        []string{"authorization", "cookie", "x-api-key"},
		FlushIntervalMs:      5000,
		MaxBatchSize:         100,
		MaxQueueSize:         5000,
		Gzip:                 true,
		MaxRetries:           3,
		RetryBaseDelayMs:     500,
		RequestTimeoutMs:     10000,
		CaptureSourceContext: true,
		ScrubSensitiveData:   true,
		UserAgent:            defaultUserAgent,
	}
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("[DevLite] init requires an `api_key` (or DEVLITE_API_KEY env var)")
	}
	if c.SampleRate < 0 || c.SampleRate > 1 {
		return fmt.Errorf("[DevLite] `sample_rate` must be between 0 and 1, got %v", c.SampleRate)
	}
	if c.Endpoint == "" {
		return errors.New("[DevLite] `endpoint` cannot be empty")
	}
	return nil
}

// Option configures a Client at construction time.
type Option func(*Config)

// WithAPIKey sets the project API key (also read from DEVLITE_API_KEY).
func WithAPIKey(key string) Option { return func(c *Config) { c.APIKey = key } }

// WithEndpoint overrides the ingest URL (defaults to production).
func WithEndpoint(u string) Option { return func(c *Config) { c.Endpoint = u } }

// WithEnvironment sets the environment tag (default: $DEVLITE_ENVIRONMENT or "development").
func WithEnvironment(e string) Option { return func(c *Config) { c.Environment = e } }

// WithServiceName sets the service name (default: $DEVLITE_SERVICE_NAME or current folder).
func WithServiceName(n string) Option { return func(c *Config) { c.ServiceName = n } }

// WithRelease sets the release/version, shown in deployment views.
func WithRelease(r string) Option { return func(c *Config) { c.Release = r } }

// WithSampleRate sets request sampling between 0.0 and 1.0. Sampling is
// coherent: a sampled-out request drops its errors/spans/logs/metrics
// together. Deployments and out-of-request crashes always survive.
func WithSampleRate(r float64) Option { return func(c *Config) { c.SampleRate = r } }

// WithCaptureBody captures (redacted) request headers — off by default.
func WithCaptureBody(b bool) Option { return func(c *Config) { c.CaptureBody = b } }

// WithRedactHeaders overrides which header names are always redacted.
func WithRedactHeaders(h []string) Option { return func(c *Config) { c.RedactHeaders = h } }

// WithFlushIntervalMs sets how often batched events are sent.
func WithFlushIntervalMs(ms int) Option { return func(c *Config) { c.FlushIntervalMs = ms } }

// WithMaxBatchSize caps events sent per request.
func WithMaxBatchSize(n int) Option { return func(c *Config) { c.MaxBatchSize = n } }

// WithMaxQueueSize caps buffered events before oldest are dropped.
func WithMaxQueueSize(n int) Option { return func(c *Config) { c.MaxQueueSize = n } }

// WithGzip enables Content-Encoding: gzip on request bodies (default on).
func WithGzip(b bool) Option { return func(c *Config) { c.Gzip = b } }

// WithDebug logs SDK internals.
func WithDebug(b bool) Option { return func(c *Config) { c.Debug = b } }

// WithMaxRetries sets delivery retries before calling OnError.
func WithMaxRetries(n int) Option { return func(c *Config) { c.MaxRetries = n } }

// WithRetryBaseDelayMs sets the initial backoff delay between retries
// (doubles each attempt).
func WithRetryBaseDelayMs(ms int) Option { return func(c *Config) { c.RetryBaseDelayMs = ms } }

// WithRequestTimeoutMs sets the per-request HTTP timeout.
func WithRequestTimeoutMs(ms int) Option { return func(c *Config) { c.RequestTimeoutMs = ms } }

// WithCaptureSourceContext attaches surrounding source lines to errors.
func WithCaptureSourceContext(b bool) Option {
	return func(c *Config) { c.CaptureSourceContext = b }
}

// WithScrubSensitiveData enables auto-redaction of emails, tokens, credit
// cards, etc. (default on).
func WithScrubSensitiveData(b bool) Option { return func(c *Config) { c.ScrubSensitiveData = b } }

// WithUserAgent overrides the User-Agent header sent to ingest.
func WithUserAgent(ua string) Option { return func(c *Config) { c.UserAgent = ua } }

// WithOnError registers a callback for SDK-internal send failures.
func WithOnError(fn func(error)) Option { return func(c *Config) { c.OnError = fn } }
