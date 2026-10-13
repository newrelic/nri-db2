package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

var logGaugeNames = []string{"logUsed", "logAvailable", "logUtilized"}

// logRow builds a MON_GET_TRANSACTION_LOG row. reads and writes differ so a column swap shows.
func logRow(used, available, reads, writes int64) queries.Row {
	return queries.Row{
		"log_reads": reads, "log_writes": writes,
		"total_log_used": used, "total_log_available": available,
	}
}

func near(got any, want float64) bool {
	g, ok := got.(float64)
	return ok && math.Abs(g-want) < 1e-9
}

// logUtilized is used / (used + available) * 100, absent when nothing is used or available.
func TestTransactionLogGauges(t *testing.T) {
	tests := []struct {
		name            string
		used, available int64
		wantUsed        float64
		wantAvailable   float64
		wantUtilized    float64 // -1 means absent
	}{
		{"before", 2_819_118, 101_526_482, 2_819_118.0 / 4096, 101_526_482.0 / 4096, 2_819_118.0 / 104_345_600 * 100},
		{"while held", 5_291_586, 99_054_014, 5_291_586.0 / 4096, 99_054_014.0 / 4096, 5_291_586.0 / 104_345_600 * 100},
		{"after rollback", 1_021_052, 103_324_548, 1_021_052.0 / 4096, 103_324_548.0 / 4096, 1_021_052.0 / 104_345_600 * 100},
		{"50 percent used", 4096 * 500, 4096 * 500, 500, 500, 50},
		{"75 percent used", 4096 * 750, 4096 * 250, 750, 250, 75},
		{"log full", 4096 * 800, 0, 800, 0, 100},
		{"infinite log space", 4096 * 800, -1, 800, -1, 0},
		{"nothing used, nothing available", 0, 0, 0, 0, -1},
		{"nothing used", 0, 4096 * 1000, 0, 1000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := identified(newEntity(t))
			TransactionLog(ms, []queries.Row{logRow(tt.used, tt.available, 11, 22)})

			if got := ms.Metrics["logUsed"]; !near(got, tt.wantUsed) {
				t.Errorf("logUsed = %v, want %v", got, tt.wantUsed)
			}
			if got := ms.Metrics["logAvailable"]; !near(got, tt.wantAvailable) {
				t.Errorf("logAvailable = %v, want %v", got, tt.wantAvailable)
			}
			got, present := ms.Metrics["logUtilized"]
			if tt.wantUtilized < 0 {
				if present {
					t.Errorf("logUtilized = %v, want absent", got)
				}
			} else if !near(got, tt.wantUtilized) {
				t.Errorf("logUtilized = %v (present %t), want %v", got, present, tt.wantUtilized)
			}
			// The four metrics that never depend on the divisor are always present.
			for _, n := range []string{"logUsed", "logAvailable", "logReads", "logWrites"} {
				if _, ok := ms.Metrics[n]; !ok {
					t.Errorf("%s missing", n)
				}
			}
		})
	}
}

func TestTransactionLogEdgeCases(t *testing.T) {
	t.Run("no rows sets nothing", func(t *testing.T) {
		ms := identified(newEntity(t))
		before := len(ms.Metrics)
		TransactionLog(ms, nil)
		if len(ms.Metrics) != before {
			t.Errorf("metrics = %v, want none added", ms.Metrics)
		}
	})
	t.Run("several rows use the first", func(t *testing.T) {
		ms := identified(newEntity(t))
		TransactionLog(ms, []queries.Row{logRow(4096*100, 4096*100, 1, 2), logRow(4096*900, 4096*100, 1, 2)})
		if got := ms.Metrics["logUtilized"]; !near(got, 50) {
			t.Errorf("logUtilized = %v, want 50 (first row)", got)
		}
	})
	t.Run("NULL used skips logUsed and logUtilized only", func(t *testing.T) {
		row := logRow(0, 4096*100, 1, 2)
		row["total_log_used"] = nil
		ms := identified(newEntity(t))
		TransactionLog(ms, []queries.Row{row})
		for _, n := range []string{"logUsed", "logUtilized"} {
			if _, ok := ms.Metrics[n]; ok {
				t.Errorf("%s must be absent", n)
			}
		}
		for _, n := range []string{"logAvailable"} {
			if _, ok := ms.Metrics[n]; !ok {
				t.Errorf("%s missing", n)
			}
		}
	})
	t.Run("NULL available skips logAvailable and logUtilized only", func(t *testing.T) {
		row := logRow(4096*100, 0, 1, 2)
		row["total_log_available"] = nil
		ms := identified(newEntity(t))
		TransactionLog(ms, []queries.Row{row})
		for _, n := range []string{"logAvailable", "logUtilized"} {
			if _, ok := ms.Metrics[n]; ok {
				t.Errorf("%s must be absent", n)
			}
		}
		if _, ok := ms.Metrics["logUsed"]; !ok {
			t.Error("logUsed missing")
		}
	})
	t.Run("NULL counters skip only themselves", func(t *testing.T) {
		row := logRow(4096*100, 4096*100, 0, 0)
		row["log_reads"], row["log_writes"] = nil, nil
		ms := identified(newEntity(t))
		TransactionLog(ms, []queries.Row{row})
		for _, n := range logGaugeNames {
			if _, ok := ms.Metrics[n]; !ok {
				t.Errorf("%s missing", n)
			}
		}
	})
	t.Run("DECIMAL text is parsed", func(t *testing.T) {
		row := queries.Row{"log_reads": "1", "log_writes": "2", "total_log_used": "409600", "total_log_available": "409600"}
		ms := identified(newEntity(t))
		TransactionLog(ms, []queries.Row{row})
		if got := ms.Metrics["logUtilized"]; !near(got, 50) {
			t.Errorf("logUtilized = %v, want 50", got)
		}
	})
}

// logReads and logWrites are PDELTA, taken from their own columns.
func TestTransactionLogCountersArePDELTA(t *testing.T) {
	defer persist.SetNow(time.Now)
	e := newEntity(t)
	at := func(sec int) { persist.SetNow(func() time.Time { return time.Unix(int64(3_000_000+sec), 0) }) }

	steps := []struct {
		name          string
		sec           int
		reads, writes int64
		wantReads     *float64 // nil means absent
		wantWrites    *float64
	}{
		{"first run reports 0", 0, 100, 1000, f(0), f(0)},
		{"same second is skipped", 0, 100, 1000, nil, nil},
		{"delta from its own column", 10, 103, 1353, f(3), f(353)},
		{"reset is dropped", 20, 5, 20, nil, nil},
		{"next run after the reset", 30, 6, 70, f(1), f(50)},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			at(s.sec)
			ms := identified(e)
			TransactionLog(ms, []queries.Row{logRow(4096*100, 4096*900, s.reads, s.writes)})
			for n, want := range map[string]*float64{"logReads": s.wantReads, "logWrites": s.wantWrites} {
				got, ok := ms.Metrics[n]
				switch {
				case want == nil && ok:
					t.Errorf("%s = %v, want absent", n, got)
				case want != nil && (!ok || got != *want):
					t.Errorf("%s = %v (present %t), want %v", n, got, ok, *want)
				}
			}
			// The gauges are not affected by the counter steps.
			if got := ms.Metrics["logUsed"]; got != 100.0 {
				t.Errorf("logUsed = %v, want 100", got)
			}
		})
	}

	t.Run("a set without a namespace attribute gets no counters", func(t *testing.T) {
		at(40)
		ms := metric.NewSet("Db2DatabaseSample", persist.NewInMemoryStore())
		TransactionLog(ms, []queries.Row{logRow(4096, 4096, 1, 2)})
		for _, n := range []string{"logReads", "logWrites"} {
			if _, ok := ms.Metrics[n]; ok {
				t.Errorf("%s must be absent without a namespace attribute", n)
			}
		}
	})
}

// Both collectors fill the same sample; all 20 metrics and the status attributes are present.
func TestDatabaseSampleHasAllMetrics(t *testing.T) {
	defer persist.SetNow(time.Now)
	e := newEntity(t)
	run := func(sec int64) *metric.Set {
		persist.SetNow(func() time.Time { return time.Unix(4_000_000+sec, 0) })
		ms := identified(e)
		Instance(ms, []queries.Row{{"total_connections": int64(4)}})
		Database(ms, []queries.Row{dbRow(10 + sec)})
		TransactionLog(ms, []queries.Row{logRow(4096*100, 4096*900, 1+sec, 1+sec)})
		return ms
	}
	run(0)
	ms := run(10)

	gauges := []string{
		"connectionActive", "applicationActive", "applicationExecuting", "backupLatest", "connectionMax",
		"lockActive", "lockPages", "lockWait", "lockWaiting", "logUsed", "logAvailable", "logUtilized",
	}
	deltas := append(append([]string{}, deltaNames...), "logReads", "logWrites")
	if len(gauges) != 12 || len(deltas) != 8 {
		t.Fatalf("fixture lists have %d gauges and %d deltas, want 12 and 8", len(gauges), len(deltas))
	}
	for _, n := range append(gauges, deltas...) {
		if _, ok := ms.Metrics[n]; !ok {
			t.Errorf("%s missing", n)
		}
	}
	for _, n := range []string{"databaseStatus", "databaseStatusCode"} {
		if _, ok := ms.Metrics[n]; !ok {
			t.Errorf("%s missing", n)
		}
	}
}
