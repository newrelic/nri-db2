package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/ibmdb/go_ibm_db"
	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"

	"github.com/newrelic/nri-db2/pkg/queries"
)

func dbErr(state string, code int) error {
	return &go_ibm_db.Error{APIName: "SQLExecute", Diag: []go_ibm_db.DiagRecord{{State: state, NativeError: code, Message: "x"}}}
}

// testRecord is what a test target saw: the pools and tablespaces it was asked for, and the number
// of rows the tracker received.
type testRecord struct {
	pools, tablespaces []string
	tracked            int
}

// testTarget returns a target (metrics and events on) whose set factories record the names.
func testTarget(t *testing.T) (target, *metric.Set, *testRecord) {
	t.Helper()
	i, err := integration.New("test", "0.0.0", integration.InMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.Entity("db2/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}
	database := e.NewMetricSet("Db2DatabaseSample")
	rec := &testRecord{}
	return target{
		metrics:  true,
		database: database,
		bufferpool: func(name string) *metric.Set {
			rec.pools = append(rec.pools, name)
			return e.NewMetricSet("Db2BufferpoolSample", attribute.Attr("bufferpoolName", name))
		},
		tablespace: func(name string) *metric.Set {
			rec.tablespaces = append(rec.tablespaces, name)
			return e.NewMetricSet("Db2TablespaceSample", attribute.Attr("tablespaceName", name))
		},
		tracker: func(rows []queries.Row) { rec.tracked += len(rows) },
	}, database, rec
}

func TestCollectCore(t *testing.T) {
	good := map[string][]queries.Row{
		queries.Instance:       {{"total_connections": int64(4)}},
		queries.Database:       {{"db_status": "ACTIVE"}},
		queries.Bufferpool:     {{"bp_name": "IBMDEFAULTBP"}, {"bp_name": "IBMSYSTEMBP4K"}},
		queries.Tablespace:     {{"tbsp_name": "SYSCATSPACE"}, {"tbsp_name": "USERSPACE1"}, {"tbsp_name": "TEMPSPACE1"}},
		queries.TransactionLog: {{"log_reads": int64(0), "log_writes": int64(5), "total_log_available": int64(3686400), "total_log_used": int64(409600)}},
	}
	upToTablespace := []string{queries.Instance, queries.Database, queries.Bufferpool, queries.Tablespace}
	all := append(slices.Clone(upToTablespace), queries.TransactionLog)
	tests := []struct {
		name        string
		failOn      map[string]error
		wantOK      bool
		wantQueries []string
		wantPools   int
		wantTbsp    int    // tablespace samples, and rows the tracker must receive
		wantMetric  string // a metric that must be present ("" for none)
		wantAbsent  string // a metric that must be absent
	}{
		{"all queries work", nil, true, all, 2, 3, "logUsed", ""},
		{
			"missing grants skip only that query and keep the session",
			map[string]error{queries.Instance: dbErr("42501", -551)},
			true, all, 2, 3, "databaseStatusCode", "connectionActive",
		},
		{
			"missing grants on the bufferpool query skip only the bufferpool samples",
			map[string]error{queries.Bufferpool: dbErr("42501", -551)},
			true, all, 0, 3, "databaseStatusCode", "",
		},
		{
			"missing grants on the tablespace query skip the samples and the tracker",
			map[string]error{queries.Tablespace: dbErr("42501", -551)},
			true, all, 2, 0, "databaseStatusCode", "",
		},
		{
			"connection error loses the session and skips the rest",
			map[string]error{queries.Instance: dbErr("08506", -30108)},
			false, []string{queries.Instance}, 0, 0, "", "databaseStatusCode",
		},
		{
			"connection error on the bufferpool query loses the session",
			map[string]error{queries.Bufferpool: dbErr("08506", -30108)},
			false, []string{queries.Instance, queries.Database, queries.Bufferpool}, 0, 0, "databaseStatusCode", "",
		},
		{
			"connection error on the tablespace query loses the session and the tracker is not called",
			map[string]error{queries.Tablespace: dbErr("08506", -30108)},
			false, upToTablespace, 2, 0, "databaseStatusCode", "logUsed",
		},
		{
			"missing grants on the log query skip only the log metrics",
			map[string]error{queries.TransactionLog: dbErr("42501", -551)},
			true, all, 2, 3, "databaseStatusCode", "logUsed",
		},
		{
			"connection error on the log query loses the session",
			map[string]error{queries.TransactionLog: dbErr("08506", -30108)},
			false, all, 2, 3, "databaseStatusCode", "logUsed",
		},
		{
			"refused mid-run loses the session",
			map[string]error{queries.Instance: dbErr("08004", -20157)},
			false, []string{queries.Instance}, 0, 0, "", "",
		},
		{
			"authentication failure mid-run loses the session",
			map[string]error{queries.Instance: dbErr("08001", -30082)},
			false, []string{queries.Instance}, 0, 0, "", "",
		},
		{
			"error on the second query still keeps the first query's metrics",
			map[string]error{queries.Database: errors.New("boom")},
			true, all, 2, 3, "connectionActive", "databaseStatusCode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran []string
			fetch := func(q string) ([]queries.Row, error) {
				ran = append(ran, q)
				if err := tt.failOn[q]; err != nil {
					return nil, err
				}
				return good[q], nil
			}
			tgt, ms, rec := testTarget(t)
			if got := collectCore(fetch, tgt); got != tt.wantOK {
				t.Errorf("collectCore() = %t, want %t", got, tt.wantOK)
			}
			if !slices.Equal(ran, tt.wantQueries) {
				t.Errorf("ran %v, want %v", ran, tt.wantQueries)
			}
			if len(rec.pools) != tt.wantPools {
				t.Errorf("created %d bufferpool samples (%v), want %d", len(rec.pools), rec.pools, tt.wantPools)
			}
			if len(rec.tablespaces) != tt.wantTbsp {
				t.Errorf("created %d tablespace samples (%v), want %d", len(rec.tablespaces), rec.tablespaces, tt.wantTbsp)
			}
			if rec.tracked != tt.wantTbsp {
				t.Errorf("tracker saw %d rows, want %d", rec.tracked, tt.wantTbsp)
			}
			if tt.wantMetric != "" {
				if _, ok := ms.Metrics[tt.wantMetric]; !ok {
					t.Errorf("%s missing", tt.wantMetric)
				}
			}
			if tt.wantAbsent != "" {
				if _, ok := ms.Metrics[tt.wantAbsent]; ok {
					t.Errorf("%s must be absent", tt.wantAbsent)
				}
			}
		})
	}
}

// Metrics and events can be requested separately.
func TestCollectCoreModes(t *testing.T) {
	good := map[string][]queries.Row{
		queries.Instance:       {{"total_connections": int64(4)}},
		queries.Database:       {{"db_status": "ACTIVE"}},
		queries.Bufferpool:     {{"bp_name": "IBMDEFAULTBP"}},
		queries.Tablespace:     {{"tbsp_name": "USERSPACE1"}},
		queries.TransactionLog: {{"log_reads": int64(0), "log_writes": int64(5), "total_log_available": int64(3686400), "total_log_used": int64(409600)}},
	}
	tests := []struct {
		name         string
		metrics      bool
		noTracker    bool
		wantQueries  []string
		wantSamples  int
		wantTracked  int
		wantDatabase bool
	}{
		{"metrics only", true, true, []string{queries.Instance, queries.Database, queries.Bufferpool, queries.Tablespace, queries.TransactionLog}, 1, 0, true},
		{"events only runs just the tablespace query", false, false, []string{queries.Tablespace}, 0, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran []string
			fetch := func(q string) ([]queries.Row, error) {
				ran = append(ran, q)
				return good[q], nil
			}
			tgt, ms, rec := testTarget(t)
			tgt.metrics = tt.metrics
			if tt.noTracker {
				tgt.tracker = nil
			}
			if !collectCore(fetch, tgt) {
				t.Fatal("collectCore() = false")
			}
			if !slices.Equal(ran, tt.wantQueries) {
				t.Errorf("ran %v, want %v", ran, tt.wantQueries)
			}
			if len(rec.tablespaces) != tt.wantSamples {
				t.Errorf("created %d tablespace samples, want %d", len(rec.tablespaces), tt.wantSamples)
			}
			if rec.tracked != tt.wantTracked {
				t.Errorf("tracker saw %d rows, want %d", rec.tracked, tt.wantTracked)
			}
			if _, ok := ms.Metrics["databaseStatusCode"]; ok != tt.wantDatabase {
				t.Errorf("databaseStatusCode present = %t, want %t", ok, tt.wantDatabase)
			}
		})
	}
}
