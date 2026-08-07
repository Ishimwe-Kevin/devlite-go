package devlite

import (
	"errors"
	"strings"
	"testing"
)

type boomError struct{ msg string }

func (e *boomError) Error() string { return e.msg }

func errBoom() error { return &boomError{msg: "boom happened"} }

func TestFingerprintStability(t *testing.T) {
	msg := "connection refused to host: 10.0.0.42 on attempt 7"
	stack := "devlite/boom.foo\n\tC:/src/app.go:42\ndevlite/boom.bar\n\tC:/src/app.go:99"

	f1 := computeFingerprint("error", msg, stack, "")
	f2 := computeFingerprint("error", "connection refused to host: 10.0.0.99 on attempt 12", stack, "")
	if f1 != f2 {
		t.Fatalf("dynamic values changed the fingerprint: %s vs %s", f1, f2)
	}

	f3 := computeFingerprint("error", "different bug", stack, "")
	if f1 == f3 {
		t.Fatal("unrelated errors grouped into one fingerprint")
	}
}

func TestFingerprintHonorsCustomKey(t *testing.T) {
	base := computeFingerprint("error", "msg", "stack", "my-key")
	if base != computeFingerprint("error", "completely different", "other stack", "my-key") {
		t.Fatal("custom key should override heuristics")
	}
}

func TestNormalizeMessage(t *testing.T) {
	if got := normalizeMessage("  failed after   5 retries   "); got != "failed after # retries" {
		t.Fatalf("normalizeMessage = %q", got)
	}
}

func TestScrub(t *testing.T) {
	v := scrub(map[string]any{
		"email":     "john@example.com",
		"cc":        "4111 1111 1111 1111",
		"jwt":       "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.sig",
		"bearer":    "Bearer dl_live_secret123",
		"apiKey":    "apikey: sk-abcdef123456",
		"nested":    map[string]any{"token": "value"},
		"list":      []any{"nested@email.com"},
		"plainText": "keep this",
	})
	m := v.(map[string]any)
	if m["plainText"] != "keep this" {
		t.Fatalf("plain text changed: %v", m["plainText"])
	}
	for _, key := range []string{"email", "cc", "jwt", "bearer", "apiKey"} {
		if s, ok := m[key].(string); !ok || strings.Contains(s, "@") || strings.Contains(s, "sk-") {
			t.Fatalf("key %q not scrubbed: %v", key, m[key])
		}
	}
	nested := m["nested"].(map[string]any)
	if nested["token"] != "[REDACTED]" && !strings.HasPrefix(nested["token"].(string), "[REDACTED") {
		t.Fatalf("nested token not scrubbed: %v", nested["token"])
	}
	list := m["list"].([]any)
	if strings.Contains(list[0].(string), "@") {
		t.Fatalf("list email not scrubbed: %v", list[0])
	}
}

func TestSourceContext(t *testing.T) {
	ctx := sourceContext("C:/definitely/not/a/file.go", 1)
	if ctx != nil {
		t.Fatalf("expected nil for missing file, got %v", ctx)
	}
}

func TestTypeOfError(t *testing.T) {
	if got := typeOfError(errBoom()); got != "boomError" {
		t.Fatalf("typeOfError = %q", got)
	}
	if got := typeOfError(errors.New("plain")); got != "errorString" {
		t.Fatalf("typeOfError(plain) = %q", got)
	}
}
