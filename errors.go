package devlite

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Error-event enrichment: grouping fingerprint, source context, scrubbing —
// mirrors devlite-python's _errors.py so errors captured by any SDK group
// into the same issue shape.

var (
	numberRE       = regexp.MustCompile(`\d+`)
	whitespaceRE   = regexp.MustCompile(`\s+`)
	frameNumbersRE = regexp.MustCompile(`:\d+`)
)

const maxFingerprintFrames = 5
const sourceContextLines = 3

// normalizeMessage collapses whitespace and replaces digits with '#' so
// dynamic values don't split issues.
func normalizeMessage(message string) string {
	message = strings.TrimSpace(numberRE.ReplaceAllString(message, "#"))
	return whitespaceRE.ReplaceAllString(message, " ")
}

// stackFrames extracts function + file tokens (no line numbers) from a
// formatted stack, up to maxFingerprintFrames.
func stackFrames(stack string) string {
	if stack == "" {
		return ""
	}
	var frames []string
	for _, line := range strings.Split(stack, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "goroutine ") {
			continue
		}
		// Frame format: "pkg.func" followed by a tab and "file:line".
		if fileIdx := strings.Index(line, "\t"); fileIdx > 0 {
			fn := line[:fileIdx]
			file := frameNumbersRE.ReplaceAllString(strings.TrimSpace(line[fileIdx:]), "")
			frames = append(frames, fn+" "+file)
		} else if strings.Contains(line, " ") {
			frames = append(frames, frameNumbersRE.ReplaceAllString(line, ""))
		}
		if len(frames) >= maxFingerprintFrames {
			break
		}
	}
	return strings.Join(frames, "|")
}

// computeFingerprint produces a stable fingerprint so the same bug groups
// into one issue. Honors an explicit custom key over heuristics.
func computeFingerprint(type_, message, stack, customKey string) string {
	basis := customKey
	if basis == "" {
		basis = fmt.Sprintf("%s::%s::%s", type_, normalizeMessage(message), stackFrames(stack))
	}
	sum := sha1.Sum([]byte(basis))
	return hex.EncodeToString(sum[:])[:16]
}

// captureStack formats the caller's stack starting `skip` frames up.
func captureStack(skip int) string {
	pcs := make([]uintptr, 50)
	n := runtime.Callers(skip, pcs)
	return formatFrames(pcs[:n])
}

// panicStack formats the current stack after a recovered panic.
func panicStack() string {
	return string(debug.Stack())
}

func formatFrames(pcs []uintptr) string {
	frames := runtime.CallersFrames(pcs)
	var b strings.Builder
	for {
		f, more := frames.Next()
		if !strings.Contains(f.Function, "runtime.") || strings.HasPrefix(f.Function, "runtime/panic") {
			fmt.Fprintf(&b, "%s\n\t%s:%d\n", f.Function, f.File, f.Line)
		}
		if !more {
			break
		}
	}
	return b.String()
}

// sourceContext reads the actual lines around a crash live from disk.
func sourceContext(file string, line int) []map[string]any {
	if file == "" || line <= 0 {
		return nil
	}
	if _, err := os.Stat(file); err != nil {
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	start := line - 1 - sourceContextLines
	if start < 0 {
		start = 0
	}
	end := line + sourceContextLines
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]map[string]any, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, map[string]any{"line": i + 1, "content": strings.TrimSuffix(lines[i], "\r")})
	}
	return out
}

// innermostFrame walks a captured stack and returns the deepest
// non-runtime frame, used for source-context lookups.
func innermostFrame(stack string) (string, int) {
	lines := strings.Split(stack, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		raw := lines[i]
		if !strings.HasPrefix(raw, "\t") {
			continue
		}
		line := strings.TrimSpace(raw)
		// Windows paths contain colons, so split on the LAST one and only
		// accept it when the remainder is an integer line number.
		idx := strings.LastIndex(line, ":")
		if idx <= 0 {
			continue
		}
		file := line[:idx]
		var ln int
		if _, err := fmt.Sscanf(line[idx+1:], "%d", &ln); err != nil || ln <= 0 {
			continue
		}
		if file != "" && !strings.Contains(file, "runtime/") {
			return file, ln
		}
	}
	return "", 0
}

// buildErrorEvent assembles a fully-enriched error event — same shape as
// the Node and Python SDKs.
func buildErrorEvent(cfg Config, err error, scope *Scope, extra map[string]any, stack string, fatal bool) map[string]any {
	name := "Error"
	message := ""
	if err != nil {
		name = typeOfError(err)
		message = err.Error()
		if message == "" {
			message = name
		}
	}
	if stack == "" {
		stack = captureStack(4)
	}

	breadcrumbs, user, tags := scope.snapshot()
	extra = cloneMap(extra)
	for k, v := range tags {
		extra[k] = v
	}
	fingerprint := computeFingerprint(name, message, stack, fmt.Sprintf("%v", extra["fingerprint"]))

	var srcCtx []map[string]any
	if cfg.CaptureSourceContext {
		file, line := innermostFrame(stack)
		srcCtx = sourceContext(file, line)
	}

	event := map[string]any{
		"type":          "error",
		"fatal":         fatal,
		"name":          name,
		"message":       message,
		"stack":         stack,
		"fingerprint":   fingerprint,
		"sourceContext": srcCtx,
		"user":          user,
		"breadcrumbs":   breadcrumbs,
		"extra":         extra,
		"timestamp":     nowMs(),
	}
	if !cfg.ScrubSensitiveData {
		return event
	}
	return scrub(event).(map[string]any)
}

// buildPanicEvent is like buildErrorEvent but for recovered panics.
func buildPanicEvent(cfg Config, value any, scope *Scope) map[string]any {
	message := fmt.Sprintf("%v", value)
	stack := panicStack()
	breadcrumbs, user, _ := scope.snapshot()
	fingerprint := computeFingerprint("panic", message, stack, "")

	event := map[string]any{
		"type":        "error",
		"fatal":       true,
		"name":        "panic",
		"message":     message,
		"stack":       stack,
		"fingerprint": fingerprint,
		"user":        user,
		"breadcrumbs": breadcrumbs,
		"extra":       map[string]any{},
		"timestamp":   nowMs(),
	}
	if !cfg.ScrubSensitiveData {
		return event
	}
	return scrub(event).(map[string]any)
}

func typeOfError(err error) string {
	if t, ok := err.(interface{ ErrorName() string }); ok {
		return t.ErrorName()
	}
	// Unwrap to the concrete type name for grouping.
	typ := fmt.Sprintf("%T", err)
	if strings.HasPrefix(typ, "*") {
		typ = strings.TrimPrefix(typ, "*")
	}
	if i := strings.LastIndex(typ, "."); i >= 0 && i < len(typ)-1 {
		typ = typ[i+1:]
	}
	return typ
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
