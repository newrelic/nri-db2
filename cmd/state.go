package main

import (
	"github.com/newrelic/infra-integrations-sdk/v3/data/event"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/metrics"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// trackStates saves the new tablespace states first and adds the events only when the save
// worked. If the save fails, the previous states are put back in the store (the SDK file store has
// already moved the new ones into the data that the next Save writes, so the later diag Save would
// persist them). The next run then finds the same change and reports it: late, but never twice.
func trackStates(tr metrics.StateTracker, add func(*event.Event) error, rows []queries.Row) {
	events, rollback := tr.TrackWithRollback(rows)
	if err := tr.Store.Save(); err != nil {
		rollback()
		log.Warn("save tablespace state file: %s: %d state-change events are held back until the next run", err, len(events))
		return
	}
	for _, ev := range events {
		if err := add(ev); err != nil {
			log.Warn("add event %q: %s", ev.Summary, err)
		}
	}
}
