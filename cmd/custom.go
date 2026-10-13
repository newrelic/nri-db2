package main

import (
	"database/sql"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/connection"
	"github.com/newrelic/nri-db2/pkg/custom"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// labeledFetchFunc runs one user query and returns its labels and rows, at most
// queries.CustomMaxRows of them. truncated is true when the query had more.
type labeledFetchFunc func(query string) (cols []string, rows [][]any, truncated bool, err error)

// sessionLabeledFetch runs user queries through the session, which owns the reconnect budget.
func sessionLabeledFetch(s *connection.Session) labeledFetchFunc {
	return func(query string) (cols []string, rows [][]any, truncated bool, err error) {
		err = s.Do(func(db *sql.DB) error {
			var err error
			cols, rows, truncated, err = queries.CollectLabeled(db, query, queries.CustomMaxRows)
			return err
		})
		return cols, rows, truncated, err
	}
}

// collectCustom runs each custom query in isolation, like collectCore: a query that fails for a
// reason other than the connection (a typo, SQL0551N, a timeout) is logged by name and the others
// still run. It returns false when the session is lost, in which case canConnect must become 0.
// Result values are never logged here.
func collectCustom(fetch labeledFetchFunc, qs []custom.Query, newSet custom.NewSet, ident []attribute.Attribute) (sessionOK bool) {
	for _, q := range qs {
		cols, rows, truncated, err := fetch(q.SQL)
		if err != nil {
			if connection.Classify(err).IsConnectionFailure() {
				log.Warn("custom query %q: connection lost: %s", q.Name, connection.Reason(err))
				return false
			}
			log.Warn("custom query %q failed, its metrics are skipped: %s", q.Name, connection.Reason(err))
			continue
		}
		if len(rows) == 0 {
			log.Debug("custom query %q returned no rows", q.Name)
			continue
		}
		if truncated {
			log.Warn("custom query %q returned more than %d rows: only the first %d are published. "+
				"Narrow it with a WHERE clause or add an ORDER BY, because a delta or rate metric restarts its "+
				"baseline when the rows inside the cap change", q.Name, queries.CustomMaxRows, len(rows))
		}
		if err := custom.Apply(q, cols, rows, newSet, ident); err != nil {
			log.Warn("custom query %q skipped: %s", q.Name, err)
		}
	}
	return true
}
