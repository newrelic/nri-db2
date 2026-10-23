package extras

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// call is one request for a sample: its name and its dimensions rendered as "name=value,...".
type call struct{ sample, dims string }

// recorder is a NewSet that keeps every set it created, in order.
type recorder struct {
	e     *integration.Entity
	calls []call
	sets  []*metric.Set
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	i, err := integration.New("test", "0.0.0", integration.InMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.Entity("db2/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}
	return &recorder{e: e}
}

func (r *recorder) newSet(sample string, dims ...attribute.Attribute) *metric.Set {
	parts := make([]string, len(dims))
	for i, d := range dims {
		parts[i] = d.Key + "=" + fmt.Sprint(d.Value)
	}
	r.calls = append(r.calls, call{sample, strings.Join(parts, ",")})
	ms := r.e.NewMetricSet(sample, append([]attribute.Attribute{attribute.Attr("database", "TESTDB")}, dims...)...)
	r.sets = append(r.sets, ms)
	return ms
}

var t0 = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

// topRow is a package-cache row. Strings are built through Normalize, as the driver's []uint8
// reaches the production code.
func topRow(over queries.Row) queries.Row {
	row := queries.Row{
		"member":              int64(0),
		"executable_id":       queries.Normalize([]byte("0100000000000000AB")),
		"section_type":        queries.Normalize([]byte("D")),
		"num_executions":      int64(4),
		"total_act_time":      int64(2000),
		"total_act_wait_time": int64(300),
		"total_cpu_time":      int64(8000000),
		"lock_wait_time":      int64(50),
		"rows_read":           int64(1000),
		"rows_returned":       int64(10),
		"stmt_text":           queries.Normalize([]byte("SELECT 1 FROM T1")),
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

func TestTopQueriesMapping(t *testing.T) {
	r := newRecorder(t)
	if n := TopQueries(queries.TopQueriesName, []queries.Row{topRow(nil)}, r.newSet, false); n != 1 {
		t.Fatalf("wrote %d samples, want 1", n)
	}
	if want := (call{Db2TopQuerySample, "executableId=0100000000000000AB,member=0"}); r.calls[0] != want {
		t.Errorf("call = %v, want %v", r.calls[0], want)
	}
	ms := r.sets[0]
	want := map[string]any{
		"statementText": "SELECT 1 FROM T1", "sectionType": "D",
		"numExecutions": 4.0, "totalActTime": 2000.0, "totalActWaitTime": 300.0,
		"totalCpuTime": 8000.0, // 8,000,000 microseconds
		"lockWaitTime": 50.0, "rowsRead": 1000.0, "rowsReturned": 10.0,
		"avgActTime": 500.0, "avgCpuTime": 2000.0,
	}
	for k, v := range want {
		if got, ok := ms.Metrics[k]; !ok || got != v {
			t.Errorf("%s = %v (present %t), want %v", k, got, ok, v)
		}
	}
	// The set also holds its namespace attributes (event_type, database, the two dimensions).
	if len(ms.Metrics) != len(want)+4 {
		t.Errorf("metrics = %v, want %v plus the identity", ms.Metrics, want)
	}
}

func TestTopQueriesAverages(t *testing.T) {
	tests := []struct {
		name       string
		over       queries.Row
		wantAvg    bool
		wantExecs  bool
		wantSample int
	}{
		{"executions", nil, true, true, 1},
		{"never executed", queries.Row{"num_executions": int64(0)}, false, true, 1},
		{"NULL executions", queries.Row{"num_executions": nil}, false, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRecorder(t)
			if n := TopQueries("q", []queries.Row{topRow(tt.over)}, r.newSet, false); n != tt.wantSample {
				t.Fatalf("wrote %d samples, want %d", n, tt.wantSample)
			}
			ms := r.sets[0]
			for _, k := range []string{"avgActTime", "avgCpuTime"} {
				if _, ok := ms.Metrics[k]; ok != tt.wantAvg {
					t.Errorf("%s present = %t, want %t", k, ok, tt.wantAvg)
				}
			}
			if _, ok := ms.Metrics["numExecutions"]; ok != tt.wantExecs {
				t.Errorf("numExecutions present = %t, want %t", ok, tt.wantExecs)
			}
		})
	}
}

func TestTopQueriesNullsAndMissingDimension(t *testing.T) {
	rows := []queries.Row{
		topRow(queries.Row{"executable_id": nil}),
		topRow(queries.Row{"executable_id": "  "}),
		topRow(queries.Row{"executable_id": "BB", "total_cpu_time": nil, "stmt_text": nil, "section_type": nil}),
		topRow(queries.Row{"executable_id": "CC"}),
		topRow(queries.Row{"executable_id": "DD", "member": nil}), // no member: skipped
	}
	r := newRecorder(t)
	if n := TopQueries("q", rows, r.newSet, false); n != 2 {
		t.Fatalf("wrote %d samples, want 2", n)
	}
	if len(r.calls) != 2 || r.calls[0].dims != "executableId=BB,member=0" || r.calls[1].dims != "executableId=CC,member=0" {
		t.Fatalf("calls = %v, want BB and CC", r.calls)
	}
	ms := r.sets[0]
	for _, k := range []string{"totalCpuTime", "avgCpuTime", "statementText", "sectionType"} {
		if _, ok := ms.Metrics[k]; ok {
			t.Errorf("%s must be skipped when its column is NULL", k)
		}
	}
	for _, k := range []string{"totalActTime", "avgActTime", "rowsRead"} {
		if _, ok := ms.Metrics[k]; !ok {
			t.Errorf("%s missing: a NULL column must only skip its own metric", k)
		}
	}
}

// The same executable ID on two members is two samples with different identities.
func TestTopQueriesSameExecutableOnTwoMembers(t *testing.T) {
	rows := []queries.Row{
		topRow(queries.Row{"member": int64(0)}),
		topRow(queries.Row{"member": int64(1)}),
	}
	r := newRecorder(t)
	if n := TopQueries("q", rows, r.newSet, false); n != 2 {
		t.Fatalf("wrote %d samples, want 2", n)
	}
	if r.calls[0] == r.calls[1] {
		t.Errorf("both members map to %v: the samples are indistinguishable", r.calls[0])
	}
}

func TestTopQueriesTruncatesText(t *testing.T) {
	r := newRecorder(t)
	row := topRow(queries.Row{"stmt_text": strings.Repeat("x", 6000) + "\xff"})
	TopQueries("q", []queries.Row{row}, r.newSet, false)
	got, _ := r.sets[0].Metrics["statementText"].(string)
	if len([]rune(got)) != queries.TextLimit {
		t.Errorf("statementText has %d runes, want %d", len([]rune(got)), queries.TextLimit)
	}
}

func activityRow(handle any, startAgo time.Duration, over queries.Row) queries.Row {
	row := queries.Row{
		"member":             int64(0),
		"application_handle": handle,
		"activity_id":        int64(1),
		"uow_id":             int64(7),
		"activity_state":     queries.Normalize([]byte("EXECUTING")),
		"activity_type":      queries.Normalize([]byte("READ_DML")),
		"application_name":   queries.Normalize([]byte("db2bp")),
		"executable_id":      queries.Normalize([]byte("AB01")),
		"local_start_time":   t0.Add(-startAgo),
		"now_ts":             t0,
		"total_cpu_time":     int64(2500000),
		"rows_read":          int64(99),
		"rows_returned":      int64(3),
		"stmt_text":          queries.Normalize([]byte("WITH R(N) AS (...) SELECT 1")),
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

func TestLongRunningMapping(t *testing.T) {
	r := newRecorder(t)
	rows := []queries.Row{activityRow(int64(4242), 90*time.Second, nil)}
	if n := LongRunning(queries.LongRunningName, rows, 60, r.newSet, false); n != 1 {
		t.Fatalf("wrote %d samples, want 1", n)
	}
	if want := (call{Db2LongRunningQuerySample, "applicationHandle=4242,uowId=7,activityId=1,member=0"}); r.calls[0] != want {
		t.Errorf("call = %v, want %v", r.calls[0], want)
	}
	ms := r.sets[0]
	want := map[string]any{
		"elapsedSeconds": 90.0, "totalCpuTime": 2500.0, "rowsRead": 99.0, "rowsReturned": 3.0,
		"activityState": "EXECUTING", "activityType": "READ_DML",
		"applicationName": "db2bp", "executableId": "AB01", "statementText": "WITH R(N) AS (...) SELECT 1",
	}
	for k, v := range want {
		if got, ok := ms.Metrics[k]; !ok || got != v {
			t.Errorf("%s = %v (present %t), want %v", k, got, ok, v)
		}
	}
	// The set also holds its namespace attributes (event_type, database, the four dimensions).
	if len(ms.Metrics) != len(want)+6 {
		t.Errorf("metrics = %v, want %v plus the identity", ms.Metrics, want)
	}
}

func TestLongRunningThreshold(t *testing.T) {
	rows := []queries.Row{
		activityRow(int64(1), 59*time.Second+999*time.Millisecond, nil), // below
		activityRow(int64(2), 60*time.Second, nil),                      // exactly the threshold
		activityRow(int64(3), 61*time.Second, nil),
		activityRow(int64(4), -17*time.Microsecond, nil), // started after NOW_TS: clamped to 0, below
		activityRow(int64(5), time.Hour, queries.Row{"local_start_time": nil}),
		activityRow(int64(6), time.Hour, queries.Row{"now_ts": nil}),
		activityRow(nil, time.Hour, nil),
	}
	r := newRecorder(t)
	if n := LongRunning("q", rows, 60, r.newSet, false); n != 2 {
		t.Fatalf("wrote %d samples, want 2 (calls %v)", n, r.calls)
	}
	if r.calls[0].dims != "applicationHandle=2,uowId=7,activityId=1,member=0" ||
		r.calls[1].dims != "applicationHandle=3,uowId=7,activityId=1,member=0" {
		t.Errorf("calls = %v, want handles 2 and 3", r.calls)
	}
	if got := r.sets[0].Metrics["elapsedSeconds"]; got != 60.0 {
		t.Errorf("elapsedSeconds = %v, want 60", got)
	}

	// A threshold of 0 reports the clamped row too.
	r = newRecorder(t)
	if n := LongRunning("q", rows[3:4], 0, r.newSet, false); n != 1 {
		t.Fatalf("threshold 0 wrote %d samples, want 1", n)
	}
	if got := r.sets[0].Metrics["elapsedSeconds"]; got != 0.0 {
		t.Errorf("clamped elapsedSeconds = %v, want 0", got)
	}
}

func TestLongRunningNulls(t *testing.T) {
	r := newRecorder(t)
	row := activityRow(int64(8), time.Minute, queries.Row{
		"total_cpu_time": nil, "stmt_text": nil, "application_name": nil,
	})
	if n := LongRunning("q", []queries.Row{row}, 60, r.newSet, false); n != 1 {
		t.Fatalf("wrote %d samples, want 1", n)
	}
	ms := r.sets[0]
	for _, k := range []string{"totalCpuTime", "statementText", "applicationName"} {
		if _, ok := ms.Metrics[k]; ok {
			t.Errorf("%s must be skipped when its column is NULL", k)
		}
	}
	for _, k := range []string{"elapsedSeconds", "rowsRead", "activityState", "activityId"} {
		if _, ok := ms.Metrics[k]; !ok {
			t.Errorf("%s missing", k)
		}
	}
}

// Concurrent activities of one application are told apart by UOW and activity ID; a row that lacks
// any part of the identity is skipped.
func TestLongRunningIdentity(t *testing.T) {
	rows := []queries.Row{
		activityRow(int64(5), time.Hour, queries.Row{"activity_id": int64(1)}),                     // the outer CALL
		activityRow(int64(5), time.Hour, queries.Row{"activity_id": int64(2)}),                     // a nested statement
		activityRow(int64(5), time.Hour, queries.Row{"uow_id": int64(8), "activity_id": int64(1)}), // a held cursor
		activityRow(int64(5), time.Hour, queries.Row{"member": int64(1)}),                          // another member
		activityRow(int64(5), time.Hour, queries.Row{"uow_id": nil}),                               // skipped
		activityRow(int64(5), time.Hour, queries.Row{"activity_id": nil}),                          // skipped
		activityRow(int64(5), time.Hour, queries.Row{"member": nil}),                               // skipped
	}
	r := newRecorder(t)
	if n := LongRunning("q", rows, 60, r.newSet, false); n != 4 {
		t.Fatalf("wrote %d samples, want 4 (calls %v)", n, r.calls)
	}
	seen := map[call]bool{}
	for _, c := range r.calls {
		if seen[c] {
			t.Errorf("identity %v is used twice", c)
		}
		seen[c] = true
	}
}

func lockRow(handle any, waited time.Duration, over queries.Row) queries.Row {
	row := queries.Row{
		"req_application_handle": handle,
		"hld_application_handle": int64(17),
		"lock_name":              queries.Normalize([]byte("02000500090000000000000052")),
		"lock_object_type":       queries.Normalize([]byte("ROW")),
		"lock_mode":              queries.Normalize([]byte("X")),
		"lock_mode_requested":    queries.Normalize([]byte("S")),
		"req_executable_id":      queries.Normalize([]byte("CD02")),
		"lock_wait_start_time":   t0.Add(-waited),
		"now_ts":                 t0,
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

func TestLockWaits(t *testing.T) {
	r := newRecorder(t)
	rows := []queries.Row{
		lockRow(int64(21), 12*time.Second+250*time.Millisecond, nil),
		lockRow(int64(22), -time.Millisecond, nil), // clamped
		lockRow(nil, time.Second, nil),             // no dimension: skipped
		lockRow(int64(23), time.Second, queries.Row{"lock_wait_start_time": nil, "hld_application_handle": nil}),
	}
	if n := LockWaits(queries.LockWaitsName, rows, r.newSet); n != 3 {
		t.Fatalf("wrote %d samples, want 3 (calls %v)", n, r.calls)
	}
	if want := (call{Db2LockWaitSample, "applicationHandle=21"}); r.calls[0] != want {
		t.Errorf("call = %v, want %v", r.calls[0], want)
	}
	want := map[string]any{
		"waitSeconds": 12.25, "holderApplicationHandle": "17", "lockName": "02000500090000000000000052",
		"lockObjectType": "ROW", "lockMode": "X", "lockModeRequested": "S", "executableId": "CD02",
	}
	for k, v := range want {
		if got, ok := r.sets[0].Metrics[k]; !ok || got != v {
			t.Errorf("%s = %v (present %t), want %v", k, got, ok, v)
		}
	}
	if got := r.sets[1].Metrics["waitSeconds"]; got != 0.0 {
		t.Errorf("clamped waitSeconds = %v, want 0", got)
	}
	for _, k := range []string{"waitSeconds", "holderApplicationHandle"} {
		if _, ok := r.sets[2].Metrics[k]; ok {
			t.Errorf("%s must be skipped when its column is NULL", k)
		}
	}
	if _, ok := r.sets[2].Metrics["lockName"]; !ok {
		t.Error("lockName missing: a NULL column must only skip its own metric")
	}
}

const (
	rawText      = "/* app=billing */ SELECT * FROM T1 WHERE NAME = 'Jane Doe' AND ID IN (1, 2, 3) AND AGE > 42"
	strippedText = "SELECT * FROM T1 WHERE NAME = ? AND ID IN (?) AND AGE > ?"
)

// With stripping on, the literals and comments of both statement texts are replaced; with it off the
// text is published as read.
func TestStatementTextStripping(t *testing.T) {
	for _, strip := range []bool{true, false} {
		want := rawText
		if strip {
			want = strippedText
		}
		r := newRecorder(t)
		TopQueries("q", []queries.Row{topRow(queries.Row{"stmt_text": rawText})}, r.newSet, strip)
		LongRunning("q", []queries.Row{activityRow(int64(1), 90*time.Second, queries.Row{"stmt_text": rawText})}, 60, r.newSet, strip)
		if len(r.sets) != 2 {
			t.Fatalf("strip=%t: %d sets, want 2", strip, len(r.sets))
		}
		for _, ms := range r.sets {
			if got := ms.Metrics["statementText"]; got != want {
				t.Errorf("strip=%t: statementText = %q, want %q", strip, got, want)
			}
		}
	}
}

// A statement that is only a comment has no text left, so the attribute is absent.
func TestStatementTextStrippedToNothing(t *testing.T) {
	r := newRecorder(t)
	TopQueries("q", []queries.Row{topRow(queries.Row{"stmt_text": "/* only a comment */"})}, r.newSet, true)
	if _, ok := r.sets[0].Metrics["statementText"]; ok {
		t.Error("statementText is set, want it absent")
	}
}

// The SQL cuts the text at TextLimit, which can leave an open string at the end. It must not leak.
func TestStatementTextCutInsideLiteral(t *testing.T) {
	r := newRecorder(t)
	cut := "SELECT * FROM T WHERE NAME = 'Jane Do"
	TopQueries("q", []queries.Row{topRow(queries.Row{"stmt_text": cut})}, r.newSet, true)
	if got := r.sets[0].Metrics["statementText"]; got != "SELECT * FROM T WHERE NAME = ?" {
		t.Errorf("statementText = %q", got)
	}
}
