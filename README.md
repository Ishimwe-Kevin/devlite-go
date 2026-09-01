# DevLite Go SDK

Official Go SDK for [DevLite](https://monitoring-devlite.andasy.dev) — AI-powered
observability. Mirror of the `@devlite/nodejs` (npm) and `devlite` (PyPI)
SDKs: add two lines, get automatic request tracking, error capture, error
grouping, source context, user tracking, and slow-endpoint detection.

Zero external dependencies. Built on `net/http`, so it covers the stdlib
server and every framework built on it (chi, Echo, Gin, gorilla/mux, ...).

## Install

```
go get github.com/Ishimwe-Kevin/devlite-go
```

## Quickstart

```go
package main

import (
    "log"
    "net/http"

    "github.com/Ishimwe-Kevin/devlite-go"
)

func main() {
    if err := devlite.Init(
        devlite.WithAPIKey("dl_..."),
        devlite.WithServiceName("my-api"),
        devlite.WithEnvironment("production"),
    ); err != nil {
        log.Fatal(err)
    }
    defer devlite.Close()

    handler := http.NewServeMux()
    handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        devlite.AddBreadcrumb(map[string]any{"type": "info", "message": "handling /"})
        devlite.SetUser(map[string]any{"id": "u-42"})
        http.Error(w, "nope", http.StatusTeapot)
    })

    log.Fatal(http.ListenAndServe(":8080", devlite.Middleware(handler)))
}
```

That's it. Every request gets tracked (method, path, status, duration),
panics are captured without being swallowed, and `SetUser` / breadcrumbs
attach to the request's error events. The User-Agent is always
`devlite-go/0.1.4` so the WAF never blocks telemetry.

## Manual API

```go
// Capture an error (a crash anywhere outside an HTTP request)
devlite.CaptureError(err, map[string]any{"route": "/checkout"})

// Within a request handler, pass ctx so spawned goroutines keep the scope:
go func() {
    devlite.CaptureErrorCtx(r.Context(), err, nil)
}()

// Non-error event
devlite.CaptureMessage("info", "payment settled", map[string]any{"order": "o-9"})

// Metric (feeds forecasting / anomaly detection)
devlite.ReportMetric("orders.minutes_to_ship", 4.5, "minute", map[string]string{"region": "eu"})

// Structured log line
devlite.CaptureLog("warn", "db slow", map[string]any{"queryMs": 3200})

// Deployment marker (powers before/after views)
devlite.ReportDeployment("1.4.2", "a1b2c3", "ship faster")

// Tracing span
span := devlite.StartSpan("render-invoice", map[string]any{"invoice": "i-7"})
// ... work ...
span.End("ok")
```

## Configuration

`Init` reads `DEVLITE_API_KEY`, `DEVLITE_ENVIRONMENT`, `DEVLITE_SERVICE_NAME`,
and `DEVLITE_RELEASE` from the environment and starts from safe defaults
(scrubbing **on**, request bodies **off**, bounded queue). Every knob is an
`Option`:

| Option | Default | Notes |
| --- | --- | --- |
| `WithAPIKey` | env | required |
| `WithEndpoint` | production | override for testing |
| `WithEnvironment` | `development` | |
| `WithServiceName` | current folder | |
| `WithRelease` | env `DEVLITE_RELEASE` | |
| `WithSampleRate` | `1.0` | coherent sampling: a sampled-out request drops its errors/spans/logs together |
| `WithCaptureBody` | `false` | redacted request headers |
| `WithRedactHeaders` | authorization, cookie, x-api-key | |
| `WithFlushIntervalMs` | `5000` | |
| `WithMaxBatchSize` | `100` | |
| `WithMaxQueueSize` | `5000` | oldest dropped when full |
| `WithGzip` | `true` | |
| `WithMaxRetries` | `3` | exponential backoff, then `OnError` |
| `WithRequestTimeoutMs` | `10000` | |
| `WithCaptureSourceContext` | `true` | surrounding source lines on errors |
| `WithScrubSensitiveData` | `true` | emails, tokens, credit cards auto-redacted |
| `WithUserAgent` | `devlite-go/0.1.4` | |
| `WithOnError` | — | callback for SDK send failures |

## Frameworks

Go web frameworks all build on `net/http`:

- **stdlib / chi**: `devlite.Middleware(mux)` or `chi.Use(devlite.Middleware)`
- **Gin**: wrap the router — `devlite.Middleware(ginEngine)` (Gin's
  `Engine` implements `http.Handler`)
- **Echo / gorilla**: use `devlite.ServeHTTPScope` inside your own middleware

For manual / non-HTTP servers (workers, cron), `CaptureError` and friends
work without any middleware — they fall back to a default scope.

## What gets sent

- `request` / `slow_request` — method, path, status, duration, headers (opt-in)
- `error` — message, stack, fingerprint, source context, user, breadcrumbs
- `panic` — recovered panics in middleware (re-raised to the server)
- `message`, `metric`, `log`, `deployment`, `span` — manual events

Errors are fingerprinted so the same bug groups into one issue across
services. User-scoped events never mix contexts across requests.

## Serverless / shutdown

Call `devlite.Flush()` before your function returns to force-send queued
events, and `devlite.Close()` at process shutdown to flush the rest.

## Development

```
go test ./...
go vet ./...
```

The test suite runs a real HTTP round trip against a mock ingest server
(checks UA, gzip, retries, coherent sampling, scrubbing, user scope, panic
capture, queue bounds, and fingerprint stability).
