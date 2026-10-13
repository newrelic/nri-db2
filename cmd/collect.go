package main

import (
	"database/sql"

	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/connection"
	"github.com/newrelic/nri-db2/pkg/metrics"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// target is where query results go: the database sample, and factories for the per-bufferpool and
// per-tablespace samples, which need the entity and the identity attributes.
type target struct {
	// metrics is false when only events were requested: then only the queries that feed events run.
	metrics    bool
	database   *metric.Set
	bufferpool metrics.NewSet
	tablespace metrics.NewSet
	// tracker is nil when events are off. It receives the rows of every successful tablespace query.
	tracker func([]queries.Row)
}

// coreQuery is one SQL query and the function that turns its rows into metrics.
type coreQuery struct {
	name  string
	sql   string
	apply func(target, []queries.Row)
	// events is true when the rows also feed an event, so the query runs without metrics.
	events bool
}

var coreQueries = []coreQuery{
	{queries.InstanceName, queries.Instance, func(t target, r []queries.Row) { metrics.Instance(t.database, r) }, false},
	{queries.DatabaseName, queries.Database, func(t target, r []queries.Row) { metrics.Database(t.database, r) }, false},
	{queries.BufferpoolName, queries.Bufferpool, func(t target, r []queries.Row) { metrics.Bufferpool(t.bufferpool, r) }, false},
	{queries.TablespaceName, queries.Tablespace, applyTablespace, true},
	{queries.TransactionLogName, queries.TransactionLog, func(t target, r []queries.Row) { metrics.TransactionLog(t.database, r) }, false},
}

// applyTablespace reports the samples when metrics are on and feeds the state tracker when events
// are on. It only runs after a successful query, so a failed query never touches the stored states.
func applyTablespace(t target, rows []queries.Row) {
	if t.metrics {
		metrics.Tablespace(t.tablespace, rows)
	}
	if t.tracker != nil {
		t.tracker(rows)
	}
}

// fetchFunc runs one query and returns its rows.
type fetchFunc func(query string) ([]queries.Row, error)

// sessionFetch runs queries through the session, which owns the reconnect budget.
func sessionFetch(s *connection.Session) fetchFunc {
	return func(query string) ([]queries.Row, error) {
		var rows []queries.Row
		err := s.Do(func(db *sql.DB) error {
			var err error
			rows, err = queries.Collect(db, query)
			return err
		})
		return rows, err
	}
}

// collectCore runs each core query in isolation: a query that fails for a reason other than the
// connection (for example SQL0551N without the grants) is logged by name and the others still
// run. It returns false when the session is lost (a connection failure, refusal or login
// failure after the reconnect budget), in which case canConnect must become 0 and the rest of
// the run is skipped.
func collectCore(fetch fetchFunc, t target) (sessionOK bool) {
	for _, q := range coreQueries {
		if !t.metrics && !q.events {
			continue
		}
		rows, err := fetch(q.sql)
		if err != nil {
			if connection.Classify(err).IsConnectionFailure() {
				log.Warn("%s: connection lost: %s", q.name, connection.Reason(err))
				return false
			}
			log.Warn("%s failed, its metrics are skipped: %s", q.name, connection.Reason(err))
			continue
		}
		q.apply(t, rows)
	}
	return true
}
