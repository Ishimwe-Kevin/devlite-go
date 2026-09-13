package devlite

import (
	"strconv"
	"strings"
)

// W3C traceparent propagation.
//
// The Node SDK injects a W3C traceparent header on every outbound request,
// so a DevLite-instrumented Node service calling a DevLite-instrumented Go
// service produces ONE continuous trace instead of two orphaned ones.
// ParseTraceparent accepts the header on inbound requests; Scope.Traceparent
// emits one for outbound calls.

// ParseTraceparent parses a W3C traceparent header
// ("<version>-<32hex trace id>-<16hex span id>-<2hex flags>").
// ok is false for missing/malformed headers. The version byte is ignored,
// mirroring the Node SDK ("00" in practice).
func ParseTraceparent(header string) (traceId, parentId string, sampled, ok bool) {
	if header == "" {
		return "", "", false, false
	}
	parts := strings.Split(header, "-")
	if len(parts) != 4 {
		return "", "", false, false
	}
	if !isHexLen(parts[1], 32) || !isHexLen(parts[2], 16) {
		return "", "", false, false
	}
	flags, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil {
		return "", "", false, false
	}
	return strings.ToLower(parts[1]), strings.ToLower(parts[2]), flags&0x01 == 0x01, true
}

// Traceparent builds a W3C traceparent header for an outbound call made from
// this scope, continuing the current trace. It returns "" when the scope has
// no W3C-form (32-hex) trace id — local traces stay internal.
func (s *Scope) Traceparent() string {
	if s == nil {
		return ""
	}
	tid := s.TraceId()
	if len(tid) != 32 {
		return ""
	}
	return "00-" + tid + "-" + newHex(8) + "-01"
}

func isHexLen(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}