package logs

import (
	"errors"
	"fmt"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/log"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// StateKey is the key of the collector state in the store. The tablespace tracker uses another key
// in the same store.
const StateKey = "diagState"

// keyLayout is fixed width, so a dedupe key can be compared with a time by its prefix.
const keyLayout = "2006-01-02T15:04:05.000000"

// FileError is a failure of the output file or the state file, as opposed to a Db2 failure. The
// caller prints it as is; Db2 errors go through connection.Reason.
type FileError struct{ Err error }

func (e *FileError) Error() string { return e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// Source reads from Db2.
type Source interface {
	// ServerNow is the Db2 server clock (CURRENT TIMESTAMP), the clock of the record timestamps.
	ServerNow() (time.Time, error)
	// Rows returns the records with TIMESTAMP >= start, ordered by TIMESTAMP. A limit above 0
	// keeps only the first limit of them; 0 returns the whole window.
	Rows(start time.Time, limit int) ([]queries.Row, error)
}

// state is what the collector keeps between runs. Times are on the server clock.
type state struct {
	// Seed is the server time of the first run. No read starts before it, so the overlap window
	// of the first real read does not reach back into history that predates the seed.
	Seed time.Time `json:"seed"`
	// Watermark is the TIMESTAMP of the last fully processed record.
	Watermark time.Time `json:"watermark"`
	// Keys are the dedupe keys of records at or after Watermark - overlap.
	Keys []string `json:"keys"`
}

// Collector turns new diag records into JSON lines. One run: read, skip what is held, write, then
// update the store. The lines are written before the state, so a crash repeats records and never
// loses them.
type Collector struct {
	Store      persist.Storer
	Source     Source
	Writer     *Writer
	ID         Identity
	Overlap    time.Duration
	MaxRecords int
}

// Result describes one run.
type Result struct {
	Seeded  bool // first run: the watermark was set and nothing was read
	Written int
	// Capped is true when the run stopped at MaxRecords with more records waiting.
	Capped bool
}

// Run does one collection. On an error the stored state is not changed, so the next run reads the
// same window again. The state is saved on every successful run, even with zero records, so the
// store TTL does not expire it.
func (c *Collector) Run() (Result, error) {
	var st state
	if _, err := c.Store.Get(StateKey, &st); err != nil || st.Watermark.IsZero() {
		if err != nil && !errors.Is(err, persist.ErrNotFound) {
			log.Warn("read stored diag state, treating this run as the first: %s", err)
		}
		return c.seed()
	}

	// The watermark is a wall-clock TIMESTAMP, and the wall clock of the server can go backwards (a
	// DST fall-back) or a record can be stamped ahead of it (another process writing in a different
	// zone). Either way the watermark would sit ahead of every new record, which would then be
	// older than the start of each read and never be collected. So it never exceeds the server clock.
	now, err := c.Source.ServerNow()
	if err != nil {
		return Result{}, fmt.Errorf("read the server clock: %w", err)
	}
	if st.Watermark.After(now) {
		log.Info("diag watermark %s is ahead of the server clock %s: the clock went back or a record is stamped in the future; reading again from the server clock",
			st.Watermark.Format(keyLayout), now.Format(keyLayout))
		st.Watermark = now
	}
	if st.Seed.After(now) {
		st.Seed = now.Add(-c.Overlap)
	}
	start := st.Watermark.Add(-c.Overlap)
	if start.Before(st.Seed) {
		start = st.Seed
	}
	held := make(map[string]struct{}, len(st.Keys))
	for _, k := range st.Keys {
		held[k] = struct{}{}
	}

	// Ask Db2 for no more than a run can write. Every held key sorts at or after start, so the
	// first limit rows hold at most len(held) records that are skipped, plus one more row than the
	// cap to tell a capped run from a complete one.
	limit := c.MaxRecords + len(held) + 1
	rows, err := c.Source.Rows(start, limit)
	if err != nil {
		return Result{}, err
	}
	// Rows that share a dedupe key are skipped too, so the slack can be used up without a single
	// new record in sight, and the watermark would never move. Then read the whole window, as an
	// unbounded read does. Nothing has been written yet, so a failure here leaves the state alone.
	if len(rows) >= limit && countNew(rows, held) <= c.MaxRecords {
		log.Info("diag read: the first %d rows held %d new records at most; reading the whole window", len(rows), c.MaxRecords)
		if rows, err = c.Source.Rows(start, 0); err != nil {
			return Result{}, err
		}
	}
	res := Result{}
	wm := st.Watermark
	opened := false
	for _, row := range rows {
		key := dedupeKey(row)
		if _, dup := held[key]; dup {
			continue
		}
		if res.Written >= c.MaxRecords {
			res.Capped = true
			break
		}
		if !opened {
			if err := c.Writer.Open(); err != nil {
				return Result{}, &FileError{err}
			}
			opened = true
		}
		if err := c.Writer.Write(Normalize(row, c.ID)); err != nil {
			_ = c.Writer.Close()
			return Result{}, &FileError{err}
		}
		held[key] = struct{}{}
		if ts, ok := row.Time("timestamp"); ok && ts.After(wm) {
			wm = ts
		}
		res.Written++
	}
	if opened {
		if err := c.Writer.Close(); err != nil {
			return Result{}, &FileError{err}
		}
	}

	if wm.After(now) {
		wm = now
	}
	cutoff := wm.Add(-c.Overlap).Format(keyLayout)
	keys := make([]string, 0, len(held))
	for k := range held {
		if len(k) >= len(cutoff) && k[:len(cutoff)] >= cutoff {
			keys = append(keys, k)
		}
	}
	st.Watermark, st.Keys = wm, keys
	return res, c.save(st)
}

// countNew counts the distinct dedupe keys in rows that are not held, which is how many records a
// run would write if it had no cap.
func countNew(rows []queries.Row, held map[string]struct{}) int {
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key := dedupeKey(row)
		if _, dup := held[key]; !dup {
			seen[key] = struct{}{}
		}
	}
	return len(seen)
}

func (c *Collector) seed() (Result, error) {
	now, err := c.Source.ServerNow()
	if err != nil {
		return Result{}, fmt.Errorf("read the server clock: %w", err)
	}
	return Result{Seeded: true}, c.save(state{Seed: now, Watermark: now})
}

func (c *Collector) save(st state) error {
	c.Store.Set(StateKey, st)
	if err := c.Store.Save(); err != nil {
		return &FileError{fmt.Errorf("save diag state: %w", err)}
	}
	return nil
}

// Watermark returns the stored watermark (server clock), or persist.ErrNotFound before the seed.
func Watermark(store persist.Storer) (time.Time, error) {
	var st state
	_, err := store.Get(StateKey, &st)
	return st.Watermark, err
}

// Describe summarises a result for a log line.
func (r Result) Describe() string {
	switch {
	case r.Seeded:
		return "first run: watermark set, no records read"
	case r.Capped:
		return fmt.Sprintf("%d records written, run capped; the rest follows on the next run", r.Written)
	default:
		return fmt.Sprintf("%d records written", r.Written)
	}
}
