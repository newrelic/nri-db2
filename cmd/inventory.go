package main

import (
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/connection"
	"github.com/newrelic/nri-db2/pkg/inventory"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// collectInventory publishes the instance and database manager configuration, the database
// configuration and the instance details as inventory items. Any failure (a missing view, a lost
// connection) is a warning and only that query's items are skipped: it never changes canConnect
// and never touches the state store or the delta baselines.
func collectInventory(fetch fetchFunc, set inventory.Setter) {
	steps := []struct {
		name, sql string
		write     func([]queries.Row) int
	}{
		{queries.DBMCfgName, queries.DBMCfg, func(r []queries.Row) int { return inventory.Parameters("dbm", r, set) }},
		{queries.DBCfgName, queries.DBCfg, func(r []queries.Row) int { return inventory.Parameters("db", r, set) }},
		{queries.InstInfoName, queries.InstInfo, func(r []queries.Row) int { return inventory.Instance(r, set) }},
	}
	for _, s := range steps {
		rows, err := fetch(s.sql)
		if err != nil {
			log.Warn("%s failed, its inventory is skipped: %s", s.name, connection.Reason(err))
			continue
		}
		if len(rows) == 0 {
			log.Warn("%s returned no rows, its inventory is skipped", s.name)
			continue
		}
		log.Debug("inventory: %s published %d items", s.name, s.write(rows))
	}
}
