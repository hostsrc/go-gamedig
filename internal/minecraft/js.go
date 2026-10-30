package minecraft

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file reproduces the bits of JavaScript semantics node-gamedig's
// result depends on. Server JSON is decoded into nil, bool, float64,
// string, []any and map[string]any; undefined marks a property JavaScript
// would read as undefined, which JSON.stringify leaves out.

type undefinedType struct{}

// undefined is JavaScript's undefined.
var undefined any = undefinedType{}

func isUndefined(v any) bool {
	_, ok := v.(undefinedType)
	return ok
}

// truthy is JavaScript's ToBoolean.
func truthy(v any) bool {
	switch v := v.(type) {
	case nil, undefinedType:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	}
	return true
}

// prop reads v[key] for a JSON value. Only objects have own properties
// that matter here; strings, numbers and arrays read as undefined for the
// keys node-gamedig uses.
func prop(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		if x, ok := m[key]; ok {
			return x
		}
	}
	return undefined
}

// jsString is JavaScript's String(v) for a JSON value.
func jsString(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case undefinedType:
		return "undefined"
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return formatNumber(v)
	case string:
		return v
	case []any:
		// Array.prototype.join: null and undefined become empty.
		parts := make([]string, len(v))
		for i, e := range v {
			if e != nil && !isUndefined(e) {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// formatNumber is JavaScript's Number.prototype.toString().
func formatNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	format := byte('f')
	if abs := math.Abs(f); abs < 1e-6 || abs >= 1e21 {
		format = 'e'
	}
	b := strconv.AppendFloat(nil, f, format, -1, 64)
	if format == 'e' {
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b = append(b[:n-2], b[n-1])
		}
	}
	return string(b)
}

// parseInt is JavaScript's parseInt(s) without a radix. It returns NaN
// when s has no leading digits.
func parseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	sign := 1.0
	if s != "" && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	base := 10
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, s = 16, s[2:]
	}
	n := 0
	for n < len(s) && digitValue(s[n]) < base {
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	if base == 10 {
		// Correctly rounded, as V8 does for decimal digit strings.
		f, _ := strconv.ParseFloat(s[:n], 64)
		return sign * f
	}
	f := 0.0
	for _, c := range []byte(s[:n]) {
		f = f*16 + float64(digitValue(c))
	}
	return sign * f
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

// isJSSpace matches JavaScript's WhiteSpace and LineTerminator characters:
// what String.prototype.trim removes and what \s matches.
func isJSSpace(r rune) bool {
	return (unicode.IsSpace(r) && r != '\u0085') || r == '\uFEFF'
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// cleanName applies node-gamedig's Minecraft name cleanup:
// name.replace(/\s+/g, ' ').replace(/§./g, ”). Without the u flag
// "." matches one UTF-16 code unit, so a § before a character outside the
// BMP removes only its high surrogate; the lone low surrogate left behind
// becomes U+FFFD once encoded as UTF-8, which is what is emitted here.
func cleanName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if isJSSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	s = b.String()
	if !strings.ContainsRune(s, '§') {
		return s
	}
	b.Reset()
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r != '§' || i >= len(s) {
			b.WriteRune(r)
			continue
		}
		next, nsize := utf8.DecodeRuneInString(s[i:])
		i += nsize
		if next > 0xFFFF {
			b.WriteRune(utf8.RuneError)
		}
	}
	return b.String()
}
