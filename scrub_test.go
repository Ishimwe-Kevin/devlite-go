package devlite

import (
	"reflect"
	"testing"
)

// TestScrubFixedSizeArrayNoPanic is a regression test for the reflect panic
// on fixed-size arrays: reflect.Value.IsNil is only valid for nilable kinds
// (Slice/Map/Ptr/Interface/etc.), so calling it on an Array panicked and
// crashed the whole scrub pass whenever an event carried an array.
func TestScrubFixedSizeArrayNoPanic(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want any
	}{
		{
			name: "fixed-size string array",
			in:   [3]string{"kevin@example.com", "hello", "secret"},
			want: []any{"[REDACTED_EMAIL]", "hello", "secret"},
		},
		{
			name: "fixed-size array of maps with sensitive keys",
			in:   [2]map[string]any{{"token": "abc", "keep": 1}, {"x": "nested@x.com"}},
			want: []any{map[string]any{"token": "[REDACTED]", "keep": 1}, map[string]any{"x": "[REDACTED_EMAIL]"}},
		},
		{
			name: "array nested in a map (event field)",
			in:   map[string]any{"tags": [2]string{"a@b.com", "plain"}},
			want: map[string]any{"tags": []any{"[REDACTED_EMAIL]", "plain"}},
		},
		{
			name: "nil typed slice stays nil",
			in:   []string(nil),
			want: nil,
		},
		{
			name: "empty fixed-size array",
			in:   [0]string{},
			want: []any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := scrub(tc.in)
			if !reflect.DeepEqual(out, tc.want) {
				t.Fatalf("scrub(%T) = %#v, want %#v", tc.in, out, tc.want)
			}
		})
	}
}
