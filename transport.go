package devlite

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Transport batches events and POSTs them to the DevLite ingest API exactly
// like the Node and Python SDKs: {apiKey, service, environment, release,
// events[]} with gzip and retry/backoff. All sends happen on a background
// goroutine so telemetry never blocks your app.
type Transport struct {
	cfg    Config
	queue  *EventQueue
	mu     sync.Mutex
	closed bool
	stop   chan struct{}
	wg     sync.WaitGroup
}

func newTransport(cfg Config) *Transport {
	t := &Transport{
		cfg:   cfg,
		queue: newEventQueue(cfg.MaxQueueSize),
		stop:  make(chan struct{}),
	}
	if cfg.FlushIntervalMs > 0 {
		t.wg.Add(1)
		go t.flusherLoop()
	}
	return t
}

func (t *Transport) Enqueue(event map[string]any) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	t.queue.Push(event)
	if t.queue.Size() >= t.cfg.MaxBatchSize {
		t.Flush()
	}
}

func (t *Transport) Flush() {
	batch := t.queue.Drain(t.cfg.MaxBatchSize)
	if len(batch) == 0 {
		return
	}
	t.sendWithRetry(batch)
}

func (t *Transport) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	close(t.stop)
	t.Flush()
	t.wg.Wait()
}

func (t *Transport) flusherLoop() {
	defer t.wg.Done()
	interval := time.Duration(t.cfg.FlushIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.Flush()
		case <-t.stop:
			return
		}
	}
}

type payload struct {
	APIKey      string           `json:"apiKey"`
	Service     string           `json:"service"`
	Environment string           `json:"environment"`
	Release     string           `json:"release"`
	Events      []map[string]any `json:"events"`
}

func (t *Transport) sendWithRetry(batch []map[string]any) {
	var err error
	for attempt := 1; attempt <= t.cfg.MaxRetries; attempt++ {
		if attempt > 1 {
			delay := time.Duration(t.cfg.RetryBaseDelayMs*(1<<(attempt-2))) * time.Millisecond
			time.Sleep(delay)
		}
		err = t.send(batch)
		if err == nil {
			return
		}
	}
	t.handleError(err, batch)
}

func (t *Transport) send(batch []map[string]any) error {
	body, err := json.Marshal(payload{
		APIKey:      t.cfg.APIKey,
		Service:     t.cfg.ServiceName,
		Environment: t.cfg.Environment,
		Release:     t.cfg.Release,
		Events:      batch,
	})
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	header := http.Header{
		"Content-Type":        []string{"application/json"},
		"User-Agent":          []string{t.cfg.UserAgent},
		"X-DevLite-Transport": []string{"1"},
	}
	if t.cfg.Gzip {
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(body); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		header.Set("Content-Encoding", "gzip")
	} else {
		buf.Write(body)
	}

	timeout := time.Duration(t.cfg.RequestTimeoutMs) * time.Millisecond
	req, err := http.NewRequest(http.MethodPost, t.cfg.Endpoint, &buf)
	if err != nil {
		return err
	}
	req.Header = header
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("DevLite ingest responded with status %d", resp.StatusCode)
	}
	return nil
}

func (t *Transport) handleError(err error, batch []map[string]any) {
	if t.cfg.Debug {
		fmt.Printf("[DevLite] failed to send %d event(s) after %d attempts: %v\n",
			len(batch), t.cfg.MaxRetries, err)
	}
	if t.cfg.OnError != nil {
		defer func() { _ = recover() }() // never let a user callback crash the SDK
		t.cfg.OnError(err)
	}
}
