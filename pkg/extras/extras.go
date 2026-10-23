// Package extras queries for top SQL, long-running SQL and lock waits and converts them into metric sets.
// Every value is a gauge or an attribute, so nothing here uses the state store or the PDELTA baselines.
package extras

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// Sample names.
const (
	Db2TopQuerySample         = "Db2TopQuerySample"
	Db2LongRunningQuerySample = "Db2LongRunningQuerySample"
	Db2LockWaitSample         = "Db2LockWaitSample"
)

// cpuMillis converts TOTAL_CPU_TIME (microseconds) to milliseconds, the unit of every time metric.
const cpuMillis = 0.001

// NewSet creates the sample for one row. The dimension attributes (i.e: executableId and
// member) join the entity identity in the metric set namespace, so each distinct combination has
// its own sample.
type NewSet func(sample string, dims ...attribute.Attribute) *metric.Set

// TopQueries writes one Db2TopQuerySample per package-cache statement, keyed by executableId and
// member, and returns the number written. The averages exist only for a statement that has run.
func TopQueries(query string, rows []queries.Row, newSet NewSet, stripLiterals bool) int {
	written := 0
	for _, row := range rows {
		b := builder{query: query, row: row, strip: stripLiterals}
		dims, ok := b.dimensions(dim{"executable_id", "executableId"}, dim{"member", "member"})
		if !ok {
			continue
		}
		b.ms = newSet(Db2TopQuerySample, dims...)
		b.text("stmt_text", "statementText")
		b.attribute("section_type", "sectionType")

		execs, okExecs := row.Float("num_executions")
		b.gauge("num_executions", "numExecutions", 1)
		b.gauge("total_act_time", "totalActTime", 1)
		b.gauge("total_act_wait_time", "totalActWaitTime", 1)
		b.gauge("total_cpu_time", "totalCpuTime", cpuMillis)
		b.gauge("lock_wait_time", "lockWaitTime", 1)
		b.gauge("rows_read", "rowsRead", 1)
		b.gauge("rows_returned", "rowsReturned", 1)
		if okExecs && execs > 0 {
			b.average("total_act_time", "avgActTime", 1, execs)
			b.average("total_cpu_time", "avgCpuTime", cpuMillis, execs)
		}
		written++
	}
	return written
}

// LongRunning writes one Db2LongRunningQuerySample per activity that has run for at least
// thresholdSeconds, keyed by applicationHandle, uowId, activityId and member, and returns the
// number written. The elapsed time is NOW_TS - LOCAL_START_TIME from the same row (one clock), never below zero.
// The threshold is checked here again, because the SQL prefilter and this check read different clocks;
// a row whose times are missing cannot be checked and is skipped.
func LongRunning(query string, rows []queries.Row, thresholdSeconds int, newSet NewSet, stripLiterals bool) int {
	written := 0
	for _, row := range rows {
		b := builder{query: query, row: row, strip: stripLiterals}
		dims, ok := b.dimensions(
			dim{"application_handle", "applicationHandle"}, dim{"uow_id", "uowId"},
			dim{"activity_id", "activityId"}, dim{"member", "member"})
		if !ok {
			continue
		}
		secs, ok := b.since("local_start_time")
		if !ok {
			log.Warn("%s returned a row without a usable start time: row skipped", query)
			continue
		}
		if secs < float64(thresholdSeconds) {
			log.Debug("%s: application %s ran %.3fs, below the %ds threshold: row skipped", query, dims[0].Value, secs, thresholdSeconds)
			continue
		}
		b.ms = newSet(Db2LongRunningQuerySample, dims...)
		b.set("elapsedSeconds", secs, metric.GAUGE)
		b.attribute("activity_state", "activityState")
		b.attribute("activity_type", "activityType")
		b.attribute("application_name", "applicationName")
		b.attribute("executable_id", "executableId")
		b.text("stmt_text", "statementText")
		b.gauge("total_cpu_time", "totalCpuTime", cpuMillis)
		b.gauge("rows_read", "rowsRead", 1)
		b.gauge("rows_returned", "rowsReturned", 1)
		written++
	}
	return written
}

// LockWaits writes one Db2LockWaitSample per waiting application, keyed by the requester's
// applicationHandle, and returns the number written. waitSeconds is NOW_TS - LOCK_WAIT_START_TIME
// from the same row, never below zero.
func LockWaits(query string, rows []queries.Row, newSet NewSet) int {
	written := 0
	for _, row := range rows {
		b := builder{query: query, row: row}
		dims, ok := b.dimensions(dim{"req_application_handle", "applicationHandle"})
		if !ok {
			continue
		}
		b.ms = newSet(Db2LockWaitSample, dims...)
		b.attribute("hld_application_handle", "holderApplicationHandle")
		b.attribute("lock_name", "lockName")
		b.attribute("lock_object_type", "lockObjectType")
		b.attribute("lock_mode", "lockMode")
		b.attribute("lock_mode_requested", "lockModeRequested")
		b.attribute("req_executable_id", "executableId")
		if secs, ok := b.since("lock_wait_start_time"); ok {
			b.set("waitSeconds", secs, metric.GAUGE)
		} else {
			log.Debug("%s: waitSeconds skipped: lock_wait_start_time or now_ts is NULL", query)
		}
		written++
	}
	return written
}

// truncate drops invalid UTF-8, keeps at most n characters (not bytes) and trims trailing
// whitespace.
func truncate(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return strings.TrimRightFunc(s, unicode.IsSpace)
}

// elapsed is the seconds from start to now, never negative: the clocks of two columns in one row
// can differ by microseconds.
func elapsed(now, start time.Time) float64 {
	return max(now.Sub(start).Seconds(), 0)
}

type builder struct {
	ms    *metric.Set
	query string
	row   queries.Row
	// strip removes literal values and comments from the statement text.
	strip bool
}

// dim names a column that is part of a sample's identity and the attribute it is published as.
type dim struct{ col, name string }

// dimensions reads the columns that key the sample, in order. A row that lacks any of them is
// skipped with a warning, because the sample could not be told apart from another.
func (b builder) dimensions(ds ...dim) ([]attribute.Attribute, bool) {
	attrs := make([]attribute.Attribute, 0, len(ds))
	for _, d := range ds {
		v, ok := b.str(d.col)
		if !ok {
			log.Warn("%s returned a row without %s: row skipped", b.query, d.col)
			return nil, false
		}
		attrs = append(attrs, attribute.Attr(d.name, v))
	}
	return attrs, true
}

// str returns a column as trimmed text. Integer ids arrive as int64 and are formatted; a NULL,
// a missing column or an empty string is not ok.
func (b builder) str(col string) (string, bool) {
	var s string
	switch x := b.row[col].(type) {
	case string:
		s = x
	case int64:
		s = strconv.FormatInt(x, 10)
	default:
		return "", false
	}
	s = strings.TrimSpace(s)
	return s, s != ""
}

// attribute sets a text attribute, or skips it when the column is NULL.
func (b builder) attribute(col, name string) {
	v, ok := b.str(col)
	if !ok {
		log.Debug("%s.%s is NULL or empty: %s skipped", b.query, col, name)
		return
	}
	b.set(name, v, metric.ATTRIBUTE)
}

// text sets the statement text, cut to queries.TextLimit characters. With strip, literal values
// and comments are removed first (see StripLiterals), so the text can only get shorter.
func (b builder) text(col, name string) {
	v, ok := b.str(col)
	if !ok {
		log.Debug("%s.%s is NULL or empty: %s skipped", b.query, col, name)
		return
	}
	if b.strip {
		v = StripLiterals(v)
	}
	if v = truncate(v, queries.TextLimit); v != "" {
		b.set(name, v, metric.ATTRIBUTE)
	}
}

// gauge sets name = column * scale, or skips it when the column is NULL.
func (b builder) gauge(col, name string, scale float64) {
	v, ok := b.row.Float(col)
	if !ok {
		log.Debug("%s.%s is NULL or not numeric: %s skipped", b.query, col, name)
		return
	}
	b.set(name, v*scale, metric.GAUGE)
}

// average sets name = column * scale / count, or skips it when the column is NULL.
func (b builder) average(col, name string, scale, count float64) {
	v, ok := b.row.Float(col)
	if !ok {
		log.Debug("%s.%s is NULL or not numeric: %s skipped", b.query, col, name)
		return
	}
	b.set(name, v*scale/count, metric.GAUGE)
}

// since returns the seconds from the timestamp column to NOW_TS, both from this row.
func (b builder) since(col string) (float64, bool) {
	start, okStart := b.row.Time(col)
	now, okNow := b.row.Time("now_ts")
	if !okStart || !okNow {
		return 0, false
	}
	return elapsed(now, start), true
}

func (b builder) set(name string, v any, t metric.SourceType) {
	if err := b.ms.SetMetric(name, v, t); err != nil {
		log.Warn("%s: set %s: %s", b.query, name, err)
	}
}
