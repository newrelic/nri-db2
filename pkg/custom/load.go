// Package custom loads user-defined SQL and maps result rows to Db2CustomSample metrics.
// It imports no cgo driver, so it is unit-testable on the host.
package custom

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultSample is the event type used when a query sets no sample_name.
const DefaultSample = "Db2CustomSample"

// InlineName is the query attribute of the CUSTOM_METRICS_QUERY statement.
const InlineName = "inline"

// Type is how a result column is published.
type Type string

// The accepted metric_types values. The SDK's pdelta and prate are not accepted: delta is
// PDELTA already, and rate is the plain RATE.
const (
	Gauge     Type = "gauge"
	Delta     Type = "delta"
	Rate      Type = "rate"
	Attribute Type = "attribute"
)

// Query is one validated custom query.
type Query struct {
	// Name is the value of the query attribute on every sample of this query.
	Name string
	SQL  string
	// Types overrides the inferred type per column label (exact, case-sensitive match).
	Types  map[string]Type
	Sample string
}

// Warn is the logging function used for rejected queries; log.Error from the SDK fits.
type Warn func(format string, args ...any)

type fileDoc struct {
	Queries []fileQuery `yaml:"queries"`
}

type fileQuery struct {
	Name        string            `yaml:"name"`
	Query       string            `yaml:"query"`
	MetricTypes map[string]string `yaml:"metric_types"`
	SampleName  string            `yaml:"sample_name"`
}

// Load returns the valid queries from the inline statement and the YAML file, inline first. A
// query that fails validation is reported through warn and skipped, as is an unreadable or
// malformed file; the other queries still load.
func Load(inline, path string, warn Warn) []Query {
	var out []Query
	names := map[string]bool{}

	if strings.TrimSpace(inline) != "" {
		if q, err := build(fileQuery{Name: InlineName, Query: inline}); err != nil {
			warn("custom query %q rejected: %s", InlineName, err)
		} else {
			names[q.Name] = true
			out = append(out, q)
		}
	}
	if path == "" {
		return out
	}

	doc, err := readFile(path)
	if err != nil {
		warn("CUSTOM_METRICS_CONFIG %s skipped: %s", path, err)
		return out
	}
	for i, fq := range doc.Queries {
		if fq.Name == "" {
			fq.Name = fmt.Sprintf("custom%d", i+1)
		}
		q, err := build(fq)
		if err == nil && names[q.Name] {
			// Two queries with one name would share their delta baselines.
			err = fmt.Errorf("duplicate query name")
		}
		if err != nil {
			warn("custom query %q rejected: %s", fq.Name, err)
			continue
		}
		names[q.Name] = true
		out = append(out, q)
	}
	return out
}

func readFile(path string) (fileDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fileDoc{}, err
	}
	var doc fileDoc
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return fileDoc{}, fmt.Errorf("parse: %w", err)
	}
	return doc, nil
}

func build(fq fileQuery) (Query, error) {
	sqlText, err := cleanSQL(fq.Query)
	if err != nil {
		return Query{}, err
	}
	q := Query{Name: fq.Name, SQL: sqlText, Sample: fq.SampleName}
	if q.Sample == "" {
		q.Sample = DefaultSample
	}
	if len(fq.MetricTypes) > 0 {
		q.Types = make(map[string]Type, len(fq.MetricTypes))
	}
	for col, name := range fq.MetricTypes {
		t, ok := parseType(name)
		if !ok {
			return Query{}, fmt.Errorf("column %q: unknown metric type %q (accepted: gauge, delta, rate, attribute)", col, name)
		}
		q.Types[col] = t
	}
	return q, nil
}

func parseType(name string) (Type, bool) {
	switch t := Type(strings.ToLower(strings.TrimSpace(name))); t {
	case Gauge, Delta, Rate, Attribute:
		return t, true
	}
	return "", false
}

// cleanSQL trims the statement, strips one trailing semicolon (the CLI may reject it) and refuses
// anything that is not a query. The monitoring user is read-only by grants; this does not rely on
// that alone.
func cleanSQL(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" {
		return "", errors.New("empty query")
	}
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
	})
	word := ""
	if len(words) > 0 {
		word = strings.ToUpper(words[0])
	}
	switch word {
	case "SELECT", "WITH", "VALUES":
	default:
		return "", fmt.Errorf("only SELECT, WITH and VALUES statements are allowed (got %q)", word)
	}
	if hasStatementSeparator(s) {
		return "", errors.New("only one statement is allowed (found ';' outside a string literal)")
	}
	if kw := dataChangeTable(s); kw != "" {
		return "", fmt.Errorf("%s TABLE runs an INSERT, UPDATE or DELETE inside a SELECT and is not allowed", kw)
	}
	return s, nil
}

// dataChangeTable returns FINAL, NEW or OLD when s uses that keyword directly before TABLE, the
// syntax that lets a SELECT change data (SELECT ... FROM FINAL TABLE (INSERT ...)). It compares
// whole words, case-insensitively, and ignores string literals, quoted identifiers and comments.
func dataChangeTable(s string) string {
	prev := ""
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"':
			i = skipQuoted(s, i)
			prev = ""
		case c == '-' && strings.HasPrefix(s[i:], "--"):
			if end := strings.IndexByte(s[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(s)
			}
		case c == '/' && strings.HasPrefix(s[i:], "/*"):
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				i = len(s)
			}
		case isWordByte(c):
			start := i
			for i < len(s) && isWordByte(s[i]) {
				i++
			}
			word := strings.ToUpper(s[start:i])
			if word == "TABLE" && (prev == "FINAL" || prev == "NEW" || prev == "OLD") {
				return prev
			}
			prev = word
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			prev = ""
			i++
		}
	}
	return ""
}

// skipQuoted returns the index after the quoted run that starts at s[i], where s[i] is the quote
// character. A doubled quote inside the run is an escaped quote. An unterminated run ends at len(s).
func skipQuoted(s string, i int) int {
	q := s[i]
	for i++; i < len(s); i++ {
		if s[i] != q {
			continue
		}
		if i+1 < len(s) && s[i+1] == q {
			i++
			continue
		}
		return i + 1
	}
	return len(s)
}

// isWordByte reports a byte that can be part of an unquoted Db2 identifier or keyword. Bytes of
// multi-byte characters count too, so they never split a word.
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c == '#' || c == '@' || c >= 0x80 ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// hasStatementSeparator reports a ';' outside single-quoted literals, which would chain a second
// statement behind the SELECT.
func hasStatementSeparator(s string) bool {
	inQuote := false
	for _, r := range s {
		switch {
		case r == '\'':
			inQuote = !inQuote
		case r == ';' && !inQuote:
			return true
		}
	}
	return false
}
