package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/event"
	"github.com/newrelic/infra-integrations-sdk/v3/log"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/metrics"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// failingSave is a store whose Save fails.
type failingSave struct{ persist.Storer }

func (failingSave) Save() error { return errors.New("disk full") }

func tbspRows(state string) []queries.Row {
	return []queries.Row{{"tbsp_name": "USERSPACE1", "tbsp_state": state}}
}

func TestTrackStates(t *testing.T) {
	var added []*event.Event
	add := func(ev *event.Event) error { added = append(added, ev); return nil }

	store := persist.NewInMemoryStore()
	tr := metrics.StateTracker{Store: store}
	trackStates(tr, add, tbspRows("NORMAL"))
	trackStates(tr, add, tbspRows("QUIESCED_SHARE"))
	if len(added) != 1 {
		t.Fatalf("%d events after one change, want 1", len(added))
	}
}

// When the state cannot be saved, no event is added; the stored states stay old, so the next
// run, with a working store, reports the change once.
func TestTrackStatesSaveFails(t *testing.T) {
	var added []*event.Event
	add := func(ev *event.Event) error { added = append(added, ev); return nil }

	store := persist.NewInMemoryStore()
	trackStates(metrics.StateTracker{Store: store}, add, tbspRows("NORMAL")) // seeds the old state

	trackStates(metrics.StateTracker{Store: failingSave{store}}, add, tbspRows("QUIESCED_SHARE"))
	if len(added) != 0 {
		t.Fatalf("%d events with a failing save, want 0", len(added))
	}
}

func TestTrackStatesAddEventFails(t *testing.T) {
	calls := 0
	add := func(*event.Event) error { calls++; return errors.New("bad") }
	store := persist.NewInMemoryStore()
	tr := metrics.StateTracker{Store: store}
	trackStates(tr, add, tbspRows("NORMAL"))
	trackStates(tr, add, tbspRows("QUIESCED_SHARE"))
	if calls != 1 {
		t.Errorf("add called %d times, want 1", calls)
	}
}

// The SDK file store moves new values into the data that a later Save writes, so a failed save
// must be rolled back: the diag step saves the same store afterwards, and it must not persist
// states whose events were never reported.
func TestTrackStatesSaveFailsFileStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "nri-db2-state-x.json")
	open := func() persist.Storer {
		s, err := persist.NewFileStore(path, log.NewStdErr(false), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	var added []*event.Event
	add := func(ev *event.Event) error { added = append(added, ev); return nil }

	// Run 1 stores NORMAL when seeded; otherwise run 2 is the first run and the rollback must delete the key.
	for _, seeded := range []bool{true, false} {
		t.Run(map[bool]string{true: "previous state", false: "first run"}[seeded], func(t *testing.T) {
			added = nil
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			store := open()
			if seeded {
				trackStates(metrics.StateTracker{Store: store}, add, tbspRows("NORMAL"))
			}

			// Run 2: the directory is gone, so the save fails.
			store = open()
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			trackStates(metrics.StateTracker{Store: store}, add, tbspRows("QUIESCED_SHARE"))
			if len(added) != 0 {
				t.Fatalf("%d events with a failing save, want 0", len(added))
			}

			// The diag step saves the same store once the disk works again.
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := store.Save(); err != nil {
				t.Fatal(err)
			}

			// Run 3 reports the change once for a stored NORMAL, and nothing on a first run.
			trackStates(metrics.StateTracker{Store: open()}, add, tbspRows("QUIESCED_SHARE"))
			want := 0
			if seeded {
				want = 1
			}
			if len(added) != want {
				t.Fatalf("%d events on the run after the failed save, want %d", len(added), want)
			}
		})
	}
}
