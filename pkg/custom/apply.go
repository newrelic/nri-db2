package custom

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"
)

// NewSet creates a sample of the given event type with the given namespace attributes. The
// attributes are written onto the sample and key the delta and rate baselines.
type NewSet func(sample string, attrs ...attribute.Attribute) *metric.Set

// reserved holds the lower-cased labels that would overwrite the identity of the sample or collide
// with attributes the SDK and the agent add. Labels are compared case-insensitively, because Db2
// upper-cases an unquoted alias.
var reserved = func() map[string]bool {
	m := map[string]bool{}
	for _, l := range []string{
		"event_type", "entityKey", "entityName", "reportingEndpoint", "db2Hostname", "port", "database", "query",
		"hostname", "host", "fullHostname", "agentName", "agentVersion", "displayName", "entityGuid", "entityId",
		"integrationName", "integrationVersion", "reportingAgent", "externalKey", "timestamp", "eventType", "eventId",
	} {
		m[strings.ToLower(l)] = true
	}
	return m
}()

type column struct {
	label string
	typ   Type
	value any
}

// Apply publishes one sample per row. cols are the original-case labels in select order and rows
// hold normalised values (nil, string, int64, float64, time.Time). ident is the identity attribute
// list shared by every sample; it is not modified.
//
// Every column typed attribute (explicit, or a string column without an override) is passed to
// newSet together with ident and the query name, so it is part of the sample and of the baseline
// key: rows of one query then keep separate delta and rate baselines. A delta or rate column in a
// row that has no attribute column is an error and the query publishes nothing, because every row
// would share one baseline.
func Apply(q Query, cols []string, rows [][]any, newSet NewSet, ident []attribute.Attribute) error {
	used := map[string]bool{}
	warned := map[string]bool{}

	classified := make([][]column, len(rows))
	for r, row := range rows {
		if len(row) != len(cols) {
			return fmt.Errorf("row %d has %d values for %d columns", r+1, len(row), len(cols))
		}
		cs := make([]column, 0, len(cols))
		attrs := 0
		for i, label := range cols {
			if reserved[strings.ToLower(label)] {
				if !warned[label] {
					warned[label] = true
					log.Warn("custom query %q: column %q is reserved and skipped: alias it", q.Name, label)
				}
				continue
			}
			if row[i] == nil {
				continue
			}
			t, explicit := q.Types[label]
			if explicit {
				used[label] = true
			} else {
				t = infer(q.Name, label, row[i], warned)
			}
			if t == Attribute {
				attrs++
			}
			cs = append(cs, column{label, t, row[i]})
		}
		if attrs == 0 {
			for _, c := range cs {
				if c.typ == Delta || c.typ == Rate {
					return fmt.Errorf("column %q is typed %s but the row has no attribute column to identify it: "+
						"select a stable string column (for example a name) or mark one with metric_types: attribute", c.label, c.typ)
				}
			}
		}
		classified[r] = cs
	}
	for label := range q.Types {
		if !used[label] && !slices.Contains(cols, label) {
			log.Warn("custom query %q: metric_types names column %q but the query returns no such column (labels are case-sensitive)", q.Name, label)
		}
	}

	for _, cs := range classified {
		attrs := append(slices.Clone(ident), attribute.Attr("query", q.Name))
		for _, c := range cs {
			if c.typ == Attribute {
				attrs = append(attrs, attribute.Attr(c.label, text(c.value)))
			}
		}
		ms := newSet(q.Sample, attrs...)
		for _, c := range cs {
			switch c.typ {
			case Gauge:
				set(ms, q.Name, c.label, c.value, metric.GAUGE)
			case Delta:
				set(ms, q.Name, c.label, c.value, metric.PDELTA)
			case Rate:
				set(ms, q.Name, c.label, c.value, metric.RATE)
			}
		}
	}
	return nil
}

// infer chooses the type of a column without an override: numbers are gauges, everything else is
// an attribute. A Go type the driver was not seen to return becomes a string attribute with one
// warning per column.
func infer(query, label string, v any, warned map[string]bool) Type {
	switch v.(type) {
	case int64, float64:
		return Gauge
	case string, time.Time:
		return Attribute
	}
	if !warned[label] {
		warned[label] = true
		log.Warn("custom query %q: column %q has unexpected type %T, published as a string attribute; "+
			"set metric_types to gauge to force a number", query, label, v)
	}
	return Attribute
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func set(ms *metric.Set, query, label string, v any, t metric.SourceType) {
	err := ms.SetMetric(label, v, t)
	switch {
	case err == nil:
	case errors.Is(err, metric.ErrNegativeDiff), errors.Is(err, metric.ErrTooCloseSamples):
		// A counter reset or two samples in the same second: expected, the next run recovers.
		log.Debug("custom query %q: %s skipped: %s", query, label, err)
	default:
		log.Warn("custom query %q: set %s: %s", query, label, err)
	}
}
