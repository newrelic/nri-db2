package metrics

import (
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

var deltaNames = []string{"connectionTotal", "lockDead", "lockTimeouts", "rowModifiedTotal", "rowReadsTotal", "rowReturnedTotal"}

func newEntity(t *testing.T) *integration.Entity {
	t.Helper()
	i, err := integration.New("test", "0.0.0", integration.InMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.Entity("db2/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func identified(e *integration.Entity) *metric.Set {
	return e.NewMetricSet("Db2DatabaseSample", attribute.Attr("database", "TESTDB"))
}

// dbRow builds a MON_GET_DATABASE row. Each counter is counter plus its own offset, so a swap of
// two columns shows up as a wrong value.
func dbRow(counter int64) queries.Row {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return queries.Row{
		"appls_cur_cons": int64(3), "appls_in_db2": int64(2), "connections_top": int64(7),
		"current_time": now, "db_status": "ACTIVE", "last_backup": now.Add(-90 * time.Second),
		"lock_list_in_use": int64(40960), "lock_wait_time": int64(10068), "lock_waits": int64(4),
		"num_locks_held": int64(9), "num_locks_waiting": int64(5),
		"deadlocks": counter + 1, "lock_timeouts": counter + 2, "rows_modified": counter + 3,
		"rows_read": counter + 4, "rows_returned": counter + 5, "total_cons": counter + 6,
	}
}

func TestDatabaseGauges(t *testing.T) {
	ms := identified(newEntity(t))
	Database(ms, []queries.Row{dbRow(10)})

	want := map[string]any{
		"applicationActive": 3.0, "applicationExecuting": 2.0, "connectionMax": 7.0,
		"lockActive": 9.0, "lockWaiting": 5.0, "lockPages": 10.0,
		"backupLatest": 90.0, "lockWait": 2517.0,
		"databaseStatus": "ACTIVE", "databaseStatusCode": 0.0,
	}
	for k, v := range want {
		if got := ms.Metrics[k]; got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
}

func TestDatabaseEdgeCases(t *testing.T) {
	t.Run("no backup is -1", func(t *testing.T) {
		row := dbRow(1)
		row["last_backup"] = nil
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if got := ms.Metrics["backupLatest"]; got != -1.0 {
			t.Errorf("backupLatest = %v, want -1", got)
		}
	})
	t.Run("NULL last_backup is -1 even without current_time", func(t *testing.T) {
		row := dbRow(1)
		row["last_backup"], row["current_time"] = nil, nil
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if got := ms.Metrics["backupLatest"]; got != -1.0 {
			t.Errorf("backupLatest = %v, want -1", got)
		}
	})
	t.Run("non-time last_backup is skipped, not -1", func(t *testing.T) {
		row := dbRow(1)
		row["last_backup"] = "2026-10-09"
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if got, ok := ms.Metrics["backupLatest"]; ok {
			t.Errorf("backupLatest = %v, want absent", got)
		}
	})
	t.Run("DECIMAL text counts as a number", func(t *testing.T) {
		row := dbRow(1)
		row["num_locks_held"] = "9.0"
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if got := ms.Metrics["lockActive"]; got != 9.0 {
			t.Errorf("lockActive = %v, want 9", got)
		}
	})
	t.Run("no lock waits gives lockWait 0", func(t *testing.T) {
		row := dbRow(1)
		row["lock_waits"], row["lock_wait_time"] = int64(0), int64(0)
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if got := ms.Metrics["lockWait"]; got != 0.0 {
			t.Errorf("lockWait = %v, want 0", got)
		}
	})
	t.Run("NULL input skips only its metrics", func(t *testing.T) {
		row := dbRow(1)
		row["num_locks_held"] = nil
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if _, ok := ms.Metrics["lockActive"]; ok {
			t.Error("lockActive must be skipped for NULL")
		}
		if _, ok := ms.Metrics["lockWaiting"]; !ok {
			t.Error("lockWaiting must still be set")
		}
	})
	t.Run("NULL status", func(t *testing.T) {
		row := dbRow(1)
		row["db_status"] = nil
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{row})
		if _, ok := ms.Metrics["databaseStatus"]; ok {
			t.Error("databaseStatus must be omitted")
		}
		if got := ms.Metrics["databaseStatusCode"]; got != 3.0 {
			t.Errorf("databaseStatusCode = %v, want 3", got)
		}
	})
	t.Run("no rows sets nothing", func(t *testing.T) {
		ms := identified(newEntity(t))
		Database(ms, nil)
		if len(ms.Metrics) != 2 { // event_type and the identity attribute only
			t.Errorf("metrics = %v, want identity attributes only", ms.Metrics)
		}
	})
	t.Run("several rows use the first", func(t *testing.T) {
		ms := identified(newEntity(t))
		Database(ms, []queries.Row{dbRow(1), dbRow(2)})
		if got := ms.Metrics["databaseStatusCode"]; got != 0.0 {
			t.Errorf("databaseStatusCode = %v, want 0", got)
		}
	})
}

// Each counter attribute must come from its own column: the second run sees the offsets as deltas.
func TestDatabaseCounterColumnMapping(t *testing.T) {
	defer persist.SetNow(time.Now)
	e := newEntity(t)
	run := func(sec int, counter int64) *metric.Set {
		persist.SetNow(func() time.Time { return time.Unix(int64(2_000_000+sec), 0) })
		ms := identified(e)
		Database(ms, []queries.Row{dbRow(counter)})
		return ms
	}
	run(0, 100)
	ms := run(10, 200) // every column grew by 100
	for _, n := range deltaNames {
		if got := ms.Metrics[n]; got != 100.0 {
			t.Errorf("%s = %v, want 100", n, got)
		}
	}
	// A column that grows alone moves only its own attribute.
	persist.SetNow(func() time.Time { return time.Unix(2_000_020, 0) })
	row := dbRow(200)
	row["rows_read"] = int64(204 + 7)
	ms = identified(e)
	Database(ms, []queries.Row{row})
	for _, n := range deltaNames {
		want := 0.0
		if n == "rowReadsTotal" {
			want = 7.0
		}
		if got := ms.Metrics[n]; got != want {
			t.Errorf("%s = %v, want %v", n, got, want)
		}
	}
}

func TestInstance(t *testing.T) {
	ms := identified(newEntity(t))
	Instance(ms, []queries.Row{{"total_connections": int64(4)}})
	if got := ms.Metrics["connectionActive"]; got != 4.0 {
		t.Errorf("connectionActive = %v, want 4", got)
	}
}

// PDELTA counter behaviour: first run, delta, same second, reset.
func TestDatabaseCountersArePDELTA(t *testing.T) {
	defer persist.SetNow(time.Now)
	e := newEntity(t)
	at := func(sec int) { persist.SetNow(func() time.Time { return time.Unix(int64(1_000_000+sec), 0) }) }

	steps := []struct {
		name    string
		sec     int
		counter int64
		want    *float64 // nil means the metric must be absent
	}{
		{"first run reports 0", 0, 1000, f(0)},
		{"same second is skipped", 0, 1000, nil},
		{"delta", 10, 1500, f(500)},
		{"reset is dropped, not -1480", 20, 20, nil},
		{"next run after the reset", 30, 70, f(50)},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			at(s.sec)
			ms := identified(e)
			Database(ms, []queries.Row{dbRow(s.counter)})
			for _, n := range deltaNames {
				got, ok := ms.Metrics[n]
				switch {
				case s.want == nil && ok:
					t.Errorf("%s = %v, want absent", n, got)
				case s.want != nil && (!ok || got != *s.want):
					t.Errorf("%s = %v (present %t), want %v", n, got, ok, *s.want)
				}
			}
		})
	}

	t.Run("a set without a namespace attribute gets no counters", func(t *testing.T) {
		at(40)
		// metric.NewSet directly: sets from an entity always carry its entityKey namespace.
		ms := metric.NewSet("Db2DatabaseSample", persist.NewInMemoryStore())
		Database(ms, []queries.Row{dbRow(100)})
		for _, n := range deltaNames {
			if _, ok := ms.Metrics[n]; ok {
				t.Errorf("%s must be absent without a namespace attribute", n)
			}
		}
	})
}

func f(v float64) *float64 { return &v }
