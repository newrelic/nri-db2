package metrics

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/integration"
	"github.com/newrelic/infra-integrations-sdk/v3/log"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

var identity = map[string]any{"db2Hostname": "h", "port": "50000", "database": "TESTDB"}

func states(kv ...string) []queries.Row {
	var rows []queries.Row
	for i := 0; i+1 < len(kv); i += 2 {
		row := queries.Row{"tbsp_name": kv[i]}
		if kv[i+1] != "<nil>" {
			row["tbsp_state"] = kv[i+1]
		}
		rows = append(rows, row)
	}
	return rows
}

func stored(t *testing.T, s persist.Storer) map[string]string {
	t.Helper()
	m := map[string]string{}
	if _, err := s.Get("tablespaceStates", &m); err != nil {
		t.Fatalf("get stored states: %s", err)
	}
	return m
}

func TestStateTracker(t *testing.T) {
	store := persist.NewInMemoryStore()
	tr := StateTracker{Store: store, Attrs: identity}

	// First run: no event, store seeded.
	if ev := tr.Track(states("SYSCATSPACE", "NORMAL", "USERSPACE1", "NORMAL")); len(ev) != 0 {
		t.Fatalf("first run gave %d events", len(ev))
	}
	if got := stored(t, store); !reflect.DeepEqual(got, map[string]string{"SYSCATSPACE": "NORMAL", "USERSPACE1": "NORMAL"}) {
		t.Fatalf("seeded map = %v", got)
	}

	// Same state: no event.
	if ev := tr.Track(states("SYSCATSPACE", "NORMAL", "USERSPACE1", "NORMAL")); len(ev) != 0 {
		t.Fatalf("unchanged run gave %d events", len(ev))
	}

	// Changed state: one event, exact summary and attributes.
	ev := tr.Track(states("SYSCATSPACE", "NORMAL", "USERSPACE1", "QUIESCED_SHARE"))
	if len(ev) != 1 {
		t.Fatalf("%d events, want 1", len(ev))
	}
	if want := "Tablespace USERSPACE1 changed state from NORMAL to QUIESCED_SHARE"; ev[0].Summary != want {
		t.Errorf("summary = %q, want %q", ev[0].Summary, want)
	}
	if ev[0].Category != "Db2TablespaceStateChange" {
		t.Errorf("category = %q", ev[0].Category)
	}
	wantAttrs := map[string]any{
		"db2Hostname": "h", "port": "50000", "database": "TESTDB",
		"tablespaceName": "USERSPACE1", "tablespaceState": "QUIESCED_SHARE", "previousTablespaceState": "NORMAL",
	}
	if !reflect.DeepEqual(ev[0].Attributes, wantAttrs) {
		t.Errorf("attributes = %v, want %v", ev[0].Attributes, wantAttrs)
	}
	if _, polluted := identity["tablespaceName"]; polluted {
		t.Error("the shared Attrs map was modified")
	}

	// Removed tablespace is pruned; a new one gives no event.
	if ev := tr.Track(states("SYSCATSPACE", "NORMAL", "NEWTS", "NORMAL")); len(ev) != 0 {
		t.Fatalf("%d events for a dropped and a new tablespace, want 0", len(ev))
	}
	if got := stored(t, store); !reflect.DeepEqual(got, map[string]string{"SYSCATSPACE": "NORMAL", "NEWTS": "NORMAL"}) {
		t.Errorf("map after prune = %v", got)
	}
}

func TestStateTrackerSequence(t *testing.T) {
	tr := StateTracker{Store: persist.NewInMemoryStore(), Attrs: identity}
	var got []string
	for _, s := range []string{"NORMAL", "NORMAL", "QUIESCED_SHARE", "QUIESCED_UPDATE", "QUIESCED_EXCLUSIVE", "QUIESCED_EXCLUSIVE", "NORMAL"} {
		for _, ev := range tr.Track(states("USERSPACE1", s)) {
			got = append(got, ev.Summary)
		}
	}
	want := []string{
		"Tablespace USERSPACE1 changed state from NORMAL to QUIESCED_SHARE",
		"Tablespace USERSPACE1 changed state from QUIESCED_SHARE to QUIESCED_UPDATE",
		"Tablespace USERSPACE1 changed state from QUIESCED_UPDATE to QUIESCED_EXCLUSIVE",
		"Tablespace USERSPACE1 changed state from QUIESCED_EXCLUSIVE to NORMAL",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestStateTrackerNullState(t *testing.T) {
	store := persist.NewInMemoryStore()
	tr := StateTracker{Store: store, Attrs: identity}
	tr.Track(states("A", "NORMAL"))
	// A NULL state is ignored: no event, and the tablespace is not remembered afterwards.
	if ev := tr.Track(states("A", "<nil>")); len(ev) != 0 {
		t.Fatalf("NULL state gave %d events", len(ev))
	}
	if ev := tr.Track(states("A", "QUIESCED_SHARE")); len(ev) != 0 {
		t.Errorf("state after a NULL gave %d events, want 0 (re-seeded)", len(ev))
	}
}

func TestStateTrackerNilAttrs(t *testing.T) {
	tr := StateTracker{Store: persist.NewInMemoryStore()}
	tr.Track(states("A", "NORMAL"))
	ev := tr.Track(states("A", "QUIESCED_SHARE"))
	if len(ev) != 1 || ev[0].Attributes == nil {
		t.Fatalf("events = %v, want one with a non-nil attribute map", ev)
	}
}

// An event reaches AddEvent, which writes the entity's custom attributes into the map: a nil
// attribute map would panic there.
func TestStateTrackerEventThroughAddEvent(t *testing.T) {
	i, err := integration.New("test", "0.0.0", integration.InMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.EntityReportedVia("agent-endpoint", "h/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}
	tr := StateTracker{Store: persist.NewInMemoryStore(), Attrs: identity}
	tr.Track(states("A", "NORMAL"))
	for _, ev := range tr.Track(states("A", "QUIESCED_SHARE")) {
		if err := e.AddEvent(ev); err != nil {
			t.Fatalf("AddEvent: %s", err)
		}
	}
	if len(e.Events) != 1 {
		t.Fatalf("%d events on the entity, want 1", len(e.Events))
	}
	if _, ok := e.Events[0].Attributes["reportingEndpoint"]; !ok {
		t.Errorf("attributes %v lack the SDK's reportingEndpoint", e.Events[0].Attributes)
	}
}

// The state survives a process restart through the file store.
func TestStateTrackerFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nri-db2-state-x.json")
	open := func() persist.Storer {
		s, err := persist.NewFileStore(path, log.NewStdErr(false), 30*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	first := open()
	StateTracker{Store: first, Attrs: identity}.Track(states("A", "NORMAL"))
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	second := open()
	ev := StateTracker{Store: second, Attrs: identity}.Track(states("A", "QUIESCED_SHARE"))
	if len(ev) != 1 {
		t.Fatalf("%d events after a reload, want 1", len(ev))
	}
}

func TestTrackWithRollbackRestoresPrevious(t *testing.T) {
	store := persist.NewInMemoryStore()
	tr := StateTracker{Store: store, Attrs: identity}
	tr.Track(states("A", "NORMAL"))

	ev, rollback := tr.TrackWithRollback(states("A", "QUIESCED_SHARE"))
	if len(ev) != 1 {
		t.Fatalf("%d events, want 1", len(ev))
	}
	if got := stored(t, store); got["A"] != "QUIESCED_SHARE" {
		t.Fatalf("stored = %v before rollback", got)
	}
	rollback()
	if got := stored(t, store); !reflect.DeepEqual(got, map[string]string{"A": "NORMAL"}) {
		t.Errorf("stored = %v after rollback, want the previous states", got)
	}
	if ev := tr.Track(states("A", "QUIESCED_SHARE")); len(ev) != 1 {
		t.Errorf("%d events after rollback, want 1 (change still pending)", len(ev))
	}
}

func TestTrackWithRollbackFirstRunDeletes(t *testing.T) {
	store := persist.NewInMemoryStore()
	tr := StateTracker{Store: store, Attrs: identity}
	_, rollback := tr.TrackWithRollback(states("A", "NORMAL"))
	rollback()
	var m map[string]string
	if _, err := store.Get("tablespaceStates", &m); !errors.Is(err, persist.ErrNotFound) {
		t.Errorf("Get after first-run rollback = %v, want ErrNotFound", err)
	}
}
