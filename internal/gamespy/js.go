package gamespy

import (
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// node-gamedig's GameSpy parsers lean on JavaScript semantics: Number() and
// parseInt() conversions, property enumeration order and the __proto__
// setter. The helpers here reproduce exactly the parts those parsers reach,
// so values and edge cases come out as they do in node.

// isJSSpace matches JavaScript's WhiteSpace and LineTerminator characters,
// which String.prototype.trim, Number() and parseInt() skip.
func isJSSpace(r rune) bool {
	return (unicode.IsSpace(r) && r != '\u0085') || r == '\uFEFF'
}

var decimalLiteral = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// toNumber is JavaScript's Number(s) for a string (StringToNumber).
func toNumber(s string) float64 {
	s = strings.TrimFunc(s, isJSSpace)
	if s == "" {
		return 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			return intValue(s[2:], base)
		}
	}
	if !decimalLiteral.MatchString(s) {
		return math.NaN()
	}
	// The literal is well formed, so the only possible error is a range
	// error, for which ParseFloat returns ±Inf or 0 as JavaScript does.
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// intValue converts a string of digits in base to the nearest float64, or
// NaN if it contains anything else.
func intValue(digits string, base int) float64 {
	if digits == "" {
		return math.NaN()
	}
	for i := 0; i < len(digits); i++ {
		if digitValue(digits[i]) >= base {
			return math.NaN()
		}
	}
	if base == 10 {
		f, _ := strconv.ParseFloat(digits, 64)
		return f
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return math.NaN()
	}
	f, _ := new(big.Float).SetInt(n).Float64()
	return f
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

// parseInt is JavaScript's parseInt(s) with no radix: leading whitespace,
// an optional sign and 0x prefix, then the longest run of digits. It
// returns NaN when there are no digits.
func parseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
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
	f := intValue(s[:n], base)
	if neg {
		f = -f
	}
	return f
}

// numberString is JavaScript's String(n).
func numberString(f float64) string {
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

// truthy is JavaScript's ToBoolean for the value types used here.
func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
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

// valueToNumber is JavaScript's Number(v); a missing value (nil) is
// undefined and gives NaN.
func valueToNumber(v any) float64 {
	switch v := v.(type) {
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case string:
		return toNumber(v)
	}
	return math.NaN()
}

// valueToString is JavaScript's String(v); a missing value is undefined.
func valueToString(v any) string {
	switch v := v.(type) {
	case nil:
		return "undefined"
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return numberString(v)
	case string:
		return v
	}
	return ""
}

// trueTest is node-gamedig's Core.trueTest.
func trueTest(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		l := strings.ToLower(v)
		return l == "true" || l == "yes" || v == "1"
	}
	return false
}

// isArrayIndex reports whether k is a canonical array index ("0" to
// "4294967294"). JavaScript enumerates such keys first, in numeric order.
func isArrayIndex(k string) (uint64, bool) {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	if err != nil || n >= math.MaxUint32 {
		return 0, false
	}
	return n, true
}

// inheritedKeys are the properties every plain JavaScript object inherits
// from Object.prototype, which the `in` operator also sees.
var inheritedKeys = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true,
	"hasOwnProperty": true, "__lookupGetter__": true, "__lookupSetter__": true,
	"isPrototypeOf": true, "propertyIsEnumerable": true, "toString": true,
	"valueOf": true, "__proto__": true, "toLocaleString": true,
}

// object is a plain JavaScript object with string keys: it remembers
// insertion order and enumerates keys as Object.keys does.
type object struct {
	order []string
	vals  map[string]entry
}

type entry struct {
	v   any
	pos int
}

func newObject() *object { return &object{vals: map[string]entry{}} }

// set is `obj[k] = v`. Assigning a primitive to __proto__ does nothing.
func (o *object) set(k string, v any) {
	if k == "__proto__" {
		return
	}
	o.define(k, v)
}

// define creates or replaces an own property, as Object.fromEntries does
// (so __proto__ becomes an ordinary key).
func (o *object) define(k string, v any) {
	if e, ok := o.vals[k]; ok {
		o.vals[k] = entry{v: v, pos: e.pos}
		return
	}
	o.vals[k] = entry{v: v, pos: len(o.order)}
	o.order = append(o.order, k)
}

func (o *object) get(k string) (any, bool) {
	e, ok := o.vals[k]
	return e.v, ok
}

func (o *object) has(k string) bool {
	_, ok := o.vals[k]
	return ok
}

func (o *object) del(k string) { delete(o.vals, k) }

func (o *object) len() int { return len(o.vals) }

// keys returns the own keys in Object.keys order: array indices ascending,
// then the other keys in insertion order.
func (o *object) keys() []string {
	var idx []string
	var rest []string
	for i, k := range o.order {
		if e, ok := o.vals[k]; !ok || e.pos != i {
			continue
		}
		if _, ok := isArrayIndex(k); ok {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := isArrayIndex(idx[i])
		b, _ := isArrayIndex(idx[j])
		return a < b
	})
	return append(idx, rest...)
}

// toMap returns the properties as a Go map.
func (o *object) toMap() map[string]any {
	m := make(map[string]any, len(o.vals))
	for k, e := range o.vals {
		m[k] = e.v
	}
	return m
}

// propertyKey is JavaScript's ToPropertyKey for a number or string.
func propertyKey(v any) string {
	if f, ok := v.(float64); ok {
		return numberString(f)
	}
	return valueToString(v)
}
