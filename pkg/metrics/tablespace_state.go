package metrics

import (
	"errors"
	"maps"

	"github.com/newrelic/infra-integrations-sdk/v3/data/event"
	"github.com/newrelic/infra-integrations-sdk/v3/log"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

const (
	// StateChangeCategory is the category of the tablespace state-change event.
	StateChangeCategory = "Db2TablespaceStateChange"

	statesKey = "tablespaceStates"
)

// StateTracker compares each tablespace state with the one stored by the previous run. The stored
// value is one map (tablespace name to state), so a dropped tablespace is pruned on the next run.
type StateTracker struct {
	// Store keeps the states between runs. The caller saves it after Track.
	Store persist.Storer
	// Attrs are copied into every event: the identity attributes db2Hostname, port and database.
	Attrs map[string]any
}

// Track returns one event per tablespace whose state changed since the previous run, then stores
// the current states. Call it only after a successful MON_GET_TABLESPACE query: the stored map is
// replaced on every call, which also refreshes its timestamp. A tablespace seen for the first
// time, or a first run, gives no event, and so does a NULL or empty state.
func (t StateTracker) Track(rows []queries.Row) []*event.Event {
	events, _ := t.TrackWithRollback(rows)
	return events
}

// TrackWithRollback is Track plus a function that puts the previous stored states back. Call it
// when the caller could not save the store: the SDK file store moves new values into the data that
// a later Save writes, so a failed save would otherwise let the next Save persist states whose
// events were never reported.
func (t StateTracker) TrackWithRollback(rows []queries.Row) ([]*event.Event, func()) {
	previous := map[string]string{}
	hadPrevious := true
	if _, err := t.Store.Get(statesKey, &previous); err != nil {
		if !errors.Is(err, persist.ErrNotFound) {
			log.Warn("read stored tablespace states, treating this run as the first: %s", err)
		}
		previous = map[string]string{}
		hadPrevious = false
	}

	var events []*event.Event
	current := make(map[string]string, len(rows))
	for _, row := range rows {
		name, okName := row.String("tbsp_name")
		state, okState := row.String("tbsp_state")
		if !okName || name == "" || !okState || state == "" {
			continue
		}
		if old, seen := previous[name]; seen && old != state {
			attrs := maps.Clone(t.Attrs)
			if attrs == nil {
				attrs = map[string]any{}
			}
			attrs["tablespaceName"] = name
			attrs["tablespaceState"] = state
			attrs["previousTablespaceState"] = old
			events = append(events, event.NewWithAttributes(
				"Tablespace "+name+" changed state from "+old+" to "+state, StateChangeCategory, attrs))
		}
		current[name] = state
	}
	t.Store.Set(statesKey, current)
	rollback := func() {
		if hadPrevious {
			t.Store.Set(statesKey, previous)
			return
		}
		_ = t.Store.Delete(statesKey)
	}
	return events, rollback
}
