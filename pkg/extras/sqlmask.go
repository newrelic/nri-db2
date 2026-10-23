package extras

import (
	"regexp"
	"strings"
)

// inListRe matches an IN list of two or more placeholders. It runs after the literals are gone, so
// it sees only `?`.
var inListRe = regexp.MustCompile(`(?i)\b(IN)\s*\(\s*\?(?:\s*,\s*\?)+\s*\)`)

// StripLiterals removes the values from SQL text so that business data in a statement is not
// published. It is a single-pass scanner, not a parser, and it works on bytes: every delimiter is
// ASCII, so UTF-8 sequences pass through intact.
//
//   - A string literal ('...', where a doubled quote is an escape, including the X, N, G, BX, GX,
//     UX and U& prefixes) and a number (123, 1.5, .5, 1E+3) become `?`.
//   - A comment (/* ... */ or -- to the end of the line) is dropped and leaves one space, which
//     absorbs the whitespace around it.
//   - An IN list of two or more placeholders becomes IN (?).
//   - Identifiers, delimited identifiers ("..."), keywords, `?` markers and :host variables are kept.
//   - An unterminated string (the SQL cuts the text, so one can end the input) becomes `?` and an
//     unterminated block comment drops the rest, so a partial value never survives.
//
// The output is never longer than the input, and stripping it again changes nothing.
func StripLiterals(s string) string {
	out := make([]byte, 0, len(s))
	gap := false // a comment was dropped and no separator is written yet
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i = skipLine(s, i)
			gap = true
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlock(s, i+2)
			gap = true
			continue
		}
		if gap {
			if isSpace(c) { // the dropped comment leaves one space, not a run
				i++
				continue
			}
			if len(out) > 0 && !isSpace(out[len(out)-1]) {
				out = append(out, ' ')
			}
			gap = false
		}
		switch {
		case c == '\'':
			i = skipString(s, i)
			out = append(out, '?')
		case c == '"':
			j := skipDelimited(s, i)
			out = append(out, s[i:j]...)
			i = j
		case isDigit(c) || (c == '.' && i+1 < len(s) && isDigit(s[i+1])):
			i = skipNumber(s, i)
			out = append(out, '?')
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			word := s[i:j]
			switch {
			case j < len(s) && s[j] == '\'' && isLiteralPrefix(word):
				i = skipString(s, j)
				out = append(out, '?')
			case j+1 < len(s) && s[j] == '&' && s[j+1] == '\'' && strings.EqualFold(word, "U"):
				i = skipString(s, j+1)
				out = append(out, '?')
			default:
				out = append(out, word...)
				i = j
			}
		default:
			out = append(out, c)
			i++
		}
	}
	if gap { // the text ended with a comment
		for len(out) > 0 && isSpace(out[len(out)-1]) {
			out = out[:len(out)-1]
		}
	}
	return inListRe.ReplaceAllString(string(out), "$1 (?)")
}

// skipLine returns the index of the newline that ends the comment starting at i, or len(s).
func skipLine(s string, i int) int {
	if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
		return i + n
	}
	return len(s)
}

// skipBlock returns the index after the "*/" that closes a comment whose body starts at from, or
// len(s) when it is not closed.
func skipBlock(s string, from int) int {
	if n := strings.Index(s[from:], "*/"); n >= 0 {
		return from + n + 2
	}
	return len(s)
}

// skipString returns the index after the string literal whose opening quote is at i, or len(s)
// when it is not closed. A doubled quote is an escaped quote.
func skipString(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] != '\'' {
			continue
		}
		if j+1 < len(s) && s[j+1] == '\'' {
			j++
			continue
		}
		return j + 1
	}
	return len(s)
}

// skipDelimited returns the index after the delimited identifier whose opening quote is at i, or
// len(s) when it is not closed. A doubled quote is an escaped quote.
func skipDelimited(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] != '"' {
			continue
		}
		if j+1 < len(s) && s[j+1] == '"' {
			j++
			continue
		}
		return j + 1
	}
	return len(s)
}

// skipNumber returns the index after the numeric literal that starts at i: digits, an optional
// fraction and an optional exponent that has digits.
func skipNumber(s string, i int) int {
	j := i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && isDigit(s[j]) {
			j++
		}
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < len(s) && isDigit(s[k]) {
			for k < len(s) && isDigit(s[k]) {
				k++
			}
			j = k
		}
	}
	return j
}

// isLiteralPrefix reports whether word, written directly before a quote, makes it a typed string
// literal: hexadecimal (X, BX), national (N), graphic (G, GX) or Unicode hexadecimal (UX).
func isLiteralPrefix(word string) bool {
	switch strings.ToUpper(word) {
	case "X", "BX", "N", "G", "GX", "UX":
		return true
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// isIdentStart accepts letters, '_', the Db2 extras '#', '@' and '$', and any byte of a multi-byte
// character.
func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '#' || c == '@' || c == '$' || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }
