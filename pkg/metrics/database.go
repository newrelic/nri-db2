// Package metrics turns query rows into typed metrics on the entity's metric sets.
package metrics

import (
	"errors"

	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/health"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// lockListPageBytes converts LOCK_LIST_IN_USE (bytes) to 4 KiB pages.
const lockListPageBytes = 4096

// Instance sets connectionActive from MON_GET_INSTANCE.
func Instance(ms *metric.Set, rows []queries.Row) {
	row, ok := firstRow(queries.InstanceName, rows)
	if !ok {
		return
	}
	b := builder{ms: ms, query: queries.InstanceName, row: row}
	b.gauge("total_connections", "connectionActive", 1)
}

// Database sets databaseStatus, databaseStatusCode and the MON_GET_DATABASE metrics. Counters are
// submitted with metric.PDELTA directly: the SDK drops a negative difference after a reset, so no
// clamp exists here.
func Database(ms *metric.Set, rows []queries.Row) {
	row, ok := firstRow(queries.DatabaseName, rows)
	if !ok {
		return
	}
	b := builder{ms: ms, query: queries.DatabaseName, row: row}

	status, present := row.String("db_status")
	if !present {
		log.Warn("%s.db_status is NULL: databaseStatus omitted, databaseStatusCode 3", queries.DatabaseName)
	}
	if err := health.SetDatabaseStatus(ms, status, present); err != nil {
		log.Warn("%s: set database status: %s", queries.DatabaseName, err)
	}

	b.gauge("appls_cur_cons", "applicationActive", 1)
	b.gauge("appls_in_db2", "applicationExecuting", 1)
	b.gauge("connections_top", "connectionMax", 1)
	b.gauge("num_locks_held", "lockActive", 1)
	b.gauge("num_locks_waiting", "lockWaiting", 1)
	b.gauge("lock_list_in_use", "lockPages", 1.0/lockListPageBytes)
	b.backupLatest()
	b.lockWait()

	b.delta("deadlocks", "lockDead")
	b.delta("lock_timeouts", "lockTimeouts")
	b.delta("rows_modified", "rowModifiedTotal")
	b.delta("rows_read", "rowReadsTotal")
	b.delta("rows_returned", "rowReturnedTotal")
	b.delta("total_cons", "connectionTotal")
}

// firstRow returns the first row, warning when there is none or more than one.
func firstRow(query string, rows []queries.Row) (queries.Row, bool) {
	switch len(rows) {
	case 0:
		log.Warn("%s returned no rows: its metrics are skipped", query)
		return nil, false
	case 1:
	default:
		log.Warn("%s returned %d rows: using the first", query, len(rows))
	}
	return rows[0], true
}

type builder struct {
	ms    *metric.Set
	query string
	row   queries.Row
}

// gauge sets name = column * scale, or skips it with a warning when the column is NULL.
func (b builder) gauge(col, name string, scale float64) {
	v, ok := b.row.Float(col)
	if !ok {
		b.skip(col, name)
		return
	}
	b.set(name, v*scale, metric.GAUGE)
}

// delta submits a cumulative counter as PDELTA.
func (b builder) delta(col, name string) {
	v, ok := b.row.Float(col)
	if !ok {
		b.skip(col, name)
		return
	}
	b.set(name, v, metric.PDELTA)
}

// backupLatest is the seconds from LAST_BACKUP to CURRENT TIMESTAMP, or -1 when no backup exists.
func (b builder) backupLatest() {
	if b.row.Absent("last_backup") {
		b.set("backupLatest", -1.0, metric.GAUGE)
		return
	}
	last, okLast := b.row.Time("last_backup")
	now, okNow := b.row.Time("current_time")
	if !okLast {
		b.skip("last_backup", "backupLatest")
		return
	}
	if !okNow {
		b.skip("current_time", "backupLatest")
		return
	}
	b.set("backupLatest", now.Sub(last).Seconds(), metric.GAUGE)
}

// lockWait is the lifetime average wait in ms: LOCK_WAIT_TIME / LOCK_WAITS, 0 when nothing waited.
func (b builder) lockWait() {
	total, okTime := b.row.Float("lock_wait_time")
	waits, okWaits := b.row.Float("lock_waits")
	if !okTime {
		b.skip("lock_wait_time", "lockWait")
		return
	}
	if !okWaits {
		b.skip("lock_waits", "lockWait")
		return
	}
	if waits == 0 {
		b.set("lockWait", 0.0, metric.GAUGE)
		return
	}
	b.set("lockWait", total/waits, metric.GAUGE)
}

func (b builder) skip(col, name string) {
	if b.row.Absent(col) {
		log.Warn("%s.%s is NULL or missing: %s skipped", b.query, col, name)
		return
	}
	log.Warn("%s.%s has unexpected type %T: %s skipped", b.query, col, b.row[col], name)
}

func (b builder) set(name string, v float64, t metric.SourceType) {
	err := b.ms.SetMetric(name, v, t)
	switch {
	case err == nil:
	case errors.Is(err, metric.ErrNegativeDiff), errors.Is(err, metric.ErrTooCloseSamples):
		// A counter reset or two samples in the same second: expected, the next run recovers.
		log.Debug("%s: %s skipped: %s", b.query, name, err)
	default:
		log.Warn("%s: set %s: %s", b.query, name, err)
	}
}
