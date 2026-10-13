// Package logs reads db2diag records through SYSPROC.PD_GET_DIAG_HIST and writes them as JSON
// lines. It depends on database/sql rows only (package queries), so it is unit-testable without
// the cgo driver.
package logs

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// LogType is the constant `logtype` attribute; the logging.d entry and the NRQL filter use it.
const LogType = "db2diag"

// Identity is the configured instance: the attributes that go on every record, equal to the ones
// on the metric samples.
type Identity struct {
	EntityName        string // HOST/DB, the entity name
	ReportingEndpoint string // HOST:PORT
	Hostname          string // db2Hostname
	Port              string
	Database          string // upper-cased
}

// Patterns for the FULLREC text. FULLREC is the exact db2diag.log record. The DBNAME and
// APPLHANDLE columns are wrong when the record's DB field is blank, so those values come from the
// text. Notify-format records (DX) have a different layout that starts with a newline: they have
// no header match, so recordId and level fall back to the columns.
var (
	// 2026-10-09-15.23.06.668395+000 I1E1840   LEVEL: Event
	headerRE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-\d{2}\.\d{2}\.\d{2}\.\d{6}[+-]\d{3,4}[ ]+(\S+)[ ]+LEVEL:[ ]*(\w+)`)
	dbRE      = regexp.MustCompile(`(?m)^INSTANCE:.*\bDB +:[ ]*(\S*)[ ]*$`)
	apphdlRE  = regexp.MustCompile(`(?m)^APPHDL +:[ ]*(\S+)`)
	retcodeRE = regexp.MustCompile(`(?m)^RETCODE :[ ]*(.*)\n?((?:[ ]{2,}\S.*\n?)*)`)
)

var levelFromColumn = map[string]string{"I": "Info", "W": "Warning", "E": "Error", "S": "Severe", "C": "Critical"}

// Normalize turns one PD_GET_DIAG_HIST row into the JSONL object. Keys with a NULL or empty value
// are omitted. At most 36 keys.
func Normalize(row queries.Row, id Identity) map[string]any {
	full, _ := row.String("fullrec")
	full = strings.ReplaceAll(full, "\x00", "")

	out := map[string]any{
		"message":           full,
		"logtype":           LogType,
		"entityName":        id.EntityName,
		"reportingEndpoint": id.ReportingEndpoint,
		"db2Hostname":       id.Hostname,
		"port":              id.Port,
		"database":          id.Database,
	}
	if ms, ok := epochMillis(row); ok {
		out["timestamp"] = ms
	}

	level := ""
	if m := headerRE.FindStringSubmatch(full); m != nil {
		out["recordId"] = m[1]
		level = m[2]
	}
	if level == "" {
		if c, ok := row.String("level"); ok {
			level = levelFromColumn[strings.ToUpper(strings.TrimSpace(c))]
		}
	}
	setStr(out, "level", level)

	if m := dbRE.FindStringSubmatch(full); m != nil {
		setStr(out, "dbName", m[1])
	}
	if m := apphdlRE.FindStringSubmatch(full); m != nil {
		out["applicationHandle"] = m[1]
	} else {
		setCol(out, row, "applicationHandle", "applhandle")
	}
	if m := retcodeRE.FindStringSubmatch(full); m != nil {
		if f := strings.Fields(m[1]); len(f) > 0 {
			out["errorCode"] = f[0]
		}
		setStr(out, "errorStack", strings.Join(strings.Fields(m[1]+" "+m[2]), " "))
	}

	for key, col := range map[string]string{
		"processName": "process_name", "instanceName": "instancename", "recordHostname": "hostname",
		"applicationId": "appl_id", "authId": "auth_id", "eduName": "eduname", "product": "product",
		"component": "component", "function": "function", "rectype": "rectype", "impact": "impact",
		"diagMessage": "msg", "calledProduct": "calledproduct", "calledComponent": "calledcomponent",
		"calledFunction": "calledfunction",
	} {
		setCol(out, row, key, col)
	}
	for key, col := range map[string]string{
		"pid": "pid", "tid": "tid", "member": "member", "dbPartitionNum": "dbpartitionnum",
		"eduId": "edu_id", "probe": "probe", "osError": "oserr",
	} {
		if n, ok := intOf(row, col); ok {
			out[key] = n
		}
	}
	return out
}

// epochMillis converts TIMESTAMP (server local time, no offset) and TIMEZONE (offset from UTC in
// minutes, positive east, so UTC = local - offset) to epoch milliseconds. Verified on Db2 12.1.5
// in several zones, including half-hour ones and DST: TIMEZONE is the offset in force when the
// record was written and equals the offset in the db2diag.log header.
//
// Db2 wraps TIMEZONE into -719..+720 minutes, so UTC+13 (Auckland in summer), UTC+13:45 and UTC+14
// arrive as -660, -615 and -600, and UTC-12 as +720. server_tz, the offset of CURRENT TIMEZONE
// (never wrapped), picks the real offset among the three that differ by a day. Without it, or when
// it is unusable, the column is taken as is.
func epochMillis(row queries.Row) (int64, bool) {
	ts, ok := row.Time("timestamp")
	if !ok {
		return 0, false
	}
	offset, _ := intOf(row, "timezone")
	if cur, ok := timezoneMinutes(row, "server_tz"); ok {
		offset = unwrapOffset(offset, cur)
	}
	utc := time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), ts.Second(), ts.Nanosecond(), time.UTC)
	return utc.Add(-time.Duration(offset) * time.Minute).UnixMilli(), true
}

// Offsets of real time zones run from UTC-12:00 to UTC+14:00.
const (
	minOffset  = -12 * 60
	maxOffset  = 14 * 60
	dayMinutes = 24 * 60
)

// unwrapOffset returns the offset, off or off +- one day inside the range of real zones, that is
// closest to cur, the server's current offset. A record and "now" are never a day of offset apart,
// so the nearest candidate is the real one.
func unwrapOffset(off, cur int64) int64 {
	best := off
	for _, c := range []int64{off - dayMinutes, off + dayMinutes} {
		if c < minOffset || c > maxOffset {
			continue
		}
		if abs64(c-cur) < abs64(best-cur) {
			best = c
		}
	}
	return best
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// timezoneMinutes reads CURRENT TIMEZONE, a duration written hhmmss (130000 for UTC+13, -40000 for
// UTC-4, 134500 for UTC+13:45), as signed minutes.
func timezoneMinutes(row queries.Row, col string) (int64, bool) {
	n, ok := intOf(row, col)
	if !ok {
		return 0, false
	}
	sign := int64(1)
	if n < 0 {
		sign, n = -1, -n
	}
	return sign * (n/10000*60 + n/100%100), true
}

func setStr(out map[string]any, key, v string) {
	if v = strings.TrimSpace(v); v != "" {
		out[key] = v
	}
}

func setCol(out map[string]any, row queries.Row, key, col string) {
	if v, ok := row.String(col); ok {
		setStr(out, key, strings.ReplaceAll(v, "\x00", ""))
	}
}

// intOf reads an integer column (int64 after Normalize, or text for a DECIMAL).
func intOf(row queries.Row, col string) (int64, bool) {
	switch x := row[col].(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// dedupeKey is (TIMESTAMP, PID, TID, RECTYPE, FUNCTION, PROBE) with the microsecond timestamp,
// followed by the first 8 hex characters of the SHA-256 of FULLREC. Two different records can share
// the six fields (one thread logging a message per object in the same microsecond); the hash keeps
// them apart. The record ID is not part of it: notify rows have none, and it is a file offset that
// repeats after a rotation. FULLREC is hashed with its NUL bytes removed, the same text that is
// written, so the key is the same on every read of the record. The timestamp stays first: the
// collector prunes held keys by that prefix.
func dedupeKey(row queries.Row) string {
	ts, _ := row.Time("timestamp")
	pid, _ := intOf(row, "pid")
	tid, _ := intOf(row, "tid")
	probe, _ := intOf(row, "probe")
	rt, _ := row.String("rectype")
	fn, _ := row.String("function")
	full, _ := row.String("fullrec")
	sum := sha256.Sum256([]byte(strings.ReplaceAll(full, "\x00", "")))
	return strings.Join([]string{
		ts.Format("2006-01-02T15:04:05.000000"),
		strconv.FormatInt(pid, 10), strconv.FormatInt(tid, 10), rt, fn, strconv.FormatInt(probe, 10),
		hex.EncodeToString(sum[:4]),
	}, "|")
}
