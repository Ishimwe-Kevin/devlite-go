package devlite

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Sensitive-data scrubbing — a faithful port of devlite-python's _scrub.py
// (which is itself a port of the Node SDK's scrub.js). Runs on every event
// regardless of config as a safety net. Deliberately conservative: better to
// over-redact than leak a credential into a dashboard.

var (
	emailRE        = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	creditCardRE   = regexp.MustCompile(`\b(?:\d[ -]*){13,16}\b`)
	jwtRE          = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	apiKeyAssignRE = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[\w\-\.]{8,}['"]?`)
	bearerTokenRE  = regexp.MustCompile(`(?i)Bearer\s+[\w\-\.]+`)
	awsKeyRE       = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
)

var sensitiveKeyNames = map[string]bool{
	"password":      true,
	"pass":          true,
	"secret":        true,
	"token":         true,
	"apikey":        true,
	"api_key":       true,
	"authorization": true,
	"cookie":        true,
	"ssn":           true,
	"creditcard":    true,
	"credit_card":   true,
}

const scrubMaxDepth = 8

func scrubString(value string) string {
	value = emailRE.ReplaceAllString(value, "[REDACTED_EMAIL]")
	value = creditCardRE.ReplaceAllString(value, "[REDACTED_CC]")
	value = jwtRE.ReplaceAllString(value, "[REDACTED_JWT]")
	value = apiKeyAssignRE.ReplaceAllString(value, "$1=[REDACTED]")
	value = bearerTokenRE.ReplaceAllString(value, "Bearer [REDACTED]")
	value = awsKeyRE.ReplaceAllString(value, "[REDACTED_AWS_KEY]")
	return value
}

func isSensitiveKey(key string) bool {
	var norm strings.Builder
	for _, r := range strings.ToLower(key) {
		if r >= 'a' && r <= 'z' {
			norm.WriteRune(r)
		}
	}
	return sensitiveKeyNames[norm.String()]
}

// scrubDeep recursively redacts strings and fully redacts sensitive-keyed
// values. Typed nil containers (e.g. a nil []map[string]any) stay nil so
// they marshal to JSON null, and JSON-friendly containers pass through
// scrubbed instead of being stringified.
func scrubDeep(value any, depth int) any {
	if depth > scrubMaxDepth {
		return value
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil
	}

	switch v := value.(type) {
	case string:
		return scrubString(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = scrubDeep(item, depth+1)
		}
		return out
	case map[string]any:
		if v == nil {
			return nil
		}
		out := make(map[string]any, len(v))
		for k, item := range v {
			if isSensitiveKey(k) {
				out[k] = "[REDACTED]"
			} else {
				out[k] = scrubDeep(item, depth+1)
			}
		}
		return out
	default:
		// Numbers, bools, and everything else that json.Marshal can handle
		// natively pass through; nil pointers/interfaces become null.
		switch rv.Kind() {
		case reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return value
		case reflect.Slice:
			if rv.IsNil() {
				return nil
			}
			out := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				out[i] = scrubDeep(rv.Index(i).Interface(), depth+1)
			}
			return out
		case reflect.Array:
			out := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				out[i] = scrubDeep(rv.Index(i).Interface(), depth+1)
			}
			return out
		case reflect.Map:
			if rv.IsNil() {
				return nil
			}
			return value
		case reflect.Ptr, reflect.Interface:
			if rv.IsNil() {
				return nil
			}
			return scrubDeep(rv.Elem().Interface(), depth+1)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
}

func scrub(v any) any { return scrubDeep(v, 0) }
