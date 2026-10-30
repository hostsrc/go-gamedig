package quake

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// isJSSpace matches JavaScript's StrWhiteSpaceChar (WhiteSpace and
// LineTerminator), which parseInt, Number() and trim() skip.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0xFEFF, 0x2028, 0x2029:
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// parseInt mirrors JavaScript's parseInt(s) without a radix: leading
// whitespace, an optional sign and an optional 0x prefix, then as many
// digits as are present. It returns NaN when there are none. Values are
// rounded to the nearest float64 like V8; overflow gives ±Inf.
func parseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	sign := 1.0
	if s != "" && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	hex := len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')
	if hex {
		s = s[2:]
	}
	n := 0
	for n < len(s) && isDigit(s[n], hex) {
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	var f float64
	if hex {
		// ParseFloat rounds hex mantissas correctly (round half to even),
		// as V8 does for power-of-two radixes.
		f, _ = strconv.ParseFloat("0x"+s[:n]+"p0", 64)
	} else {
		f, _ = strconv.ParseFloat(s[:n], 64)
	}
	// ParseFloat returns ±Inf with ErrRange on overflow, which is also
	// JavaScript's result.
	return sign * f
}

func isDigit(c byte, hex bool) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'):
		return true
	}
	return false
}

// isNumeric reports whether JavaScript's isNaN(s) is false for the string
// s, that is whether Number(s) is not NaN: whitespace only, a decimal
// literal (optionally signed, or ±Infinity), or an unsigned 0x/0o/0b
// integer.
func isNumeric(s string) bool {
	s = strings.TrimFunc(s, isJSSpace)
	if s == "" {
		return true
	}
	if len(s) > 2 && s[0] == '0' {
		var ok func(byte) bool
		switch s[1] {
		case 'x', 'X':
			ok = func(c byte) bool { return isDigit(c, true) }
		case 'o', 'O':
			ok = func(c byte) bool { return c >= '0' && c <= '7' }
		case 'b', 'B':
			ok = func(c byte) bool { return c == '0' || c == '1' }
		}
		if ok != nil {
			for i := 2; i < len(s); i++ {
				if !ok(s[i]) {
					return false
				}
			}
			return true
		}
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	if s == "Infinity" {
		return true
	}
	// digits [. digits] [e sign digits], with digits on at least one side
	// of the point.
	i, intDigits := 0, 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		intDigits++
	}
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			fracDigits++
		}
	}
	if intDigits+fracDigits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expDigits := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return false
		}
	}
	return i == len(s)
}
