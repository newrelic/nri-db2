package main

import (
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/config"
	"github.com/newrelic/nri-db2/pkg/connection"
	"github.com/newrelic/nri-db2/pkg/extras"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// collectExtras runs the enabled New Relic extras collectors in order: top SQL, long-running SQL,
// lock waits. Like collectCore, a query that fails for a reason other than the connection (for
// example SQL0551N without EXECUTE on the table function) is logged by name and the others still
// run. It returns false when the session is lost, in which case canConnect must become 0. It never
// touches the state store: every extras value is a gauge or an attribute.
func collectExtras(fetch fetchFunc, cfg config.Config, newSet extras.NewSet) (sessionOK bool) {
	steps := []struct {
		enabled   bool
		name, sql string
		write     func([]queries.Row) int
	}{
		{
			cfg.CollectTopQueries, queries.TopQueriesName, queries.TopQueries(cfg.TopQueriesLimit),
			func(r []queries.Row) int { return extras.TopQueries(queries.TopQueriesName, r, newSet) },
		},
		{
			cfg.CollectLongRunningQueries, queries.LongRunningName, queries.LongRunning(cfg.LongRunningThresholdSeconds),
			func(r []queries.Row) int {
				return extras.LongRunning(queries.LongRunningName, r, cfg.LongRunningThresholdSeconds, newSet)
			},
		},
		{
			cfg.CollectLockWaits, queries.LockWaitsName, queries.LockWaits,
			func(r []queries.Row) int { return extras.LockWaits(queries.LockWaitsName, r, newSet) },
		},
	}
	for _, s := range steps {
		if !s.enabled {
			continue
		}
		rows, err := fetch(s.sql)
		if err != nil {
			if connection.Classify(err).IsConnectionFailure() {
				log.Warn("%s: connection lost: %s", s.name, connection.Reason(err))
				return false
			}
			log.Warn("%s failed, its samples are skipped: %s", s.name, connection.Reason(err))
			continue
		}
		if len(rows) == 0 {
			log.Debug("extras: %s returned no rows", s.name)
			continue
		}
		log.Debug("extras: %s wrote %d samples", s.name, s.write(rows))
	}
	return true
}
