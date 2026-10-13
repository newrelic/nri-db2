package logs

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

var t0 = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

// fakeSource serves the rows with TIMESTAMP >= start, as the SQL does.
type fakeSource struct {
	now    time.Time
	nowErr error
	rows   []queries.Row
	err    error
	starts []time.Time
	limits []int
}

func (f *fakeSource) ServerNow() (time.Time, error) { return f.now, f.nowErr }

func (f *fakeSource) Rows(start time.Time, limit int) ([]queries.Row, error) {
	f.starts = append(f.starts, start)
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	var out []queries.Row
	for _, r := range f.rows {
		if ts := r["timestamp"].(time.Time); !ts.Before(start) {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func rec(at time.Duration, probe int64) queries.Row {
	return queries.Row{
		"timestamp": t0.Add(at), "pid": int64(7), "tid": int64(9), "rectype": "E", "function": "f", "probe": probe,
		"fullrec": "2026-10-09-15.00.00.000000+000 I1E1   LEVEL: Error\nPID : 7\n",
	}
}

func newCollector(t *testing.T, src Source) (*Collector, string) {
	t.Helper()
	dir := t.TempDir()
	return &Collector{
		Store:  persist.NewInMemoryStore(),
		Source: src,
		Writer: &Writer{Dir: dir, Name: FileName(testID), MaxBytes: 1 << 20},
		ID:     testID, Overlap: 10 * time.Second, MaxRecords: 100,
	}, filepath.Join(dir, FileName(testID))
}

func lines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); n++ {
	}
	return n
}

func TestSeedWritesNothingAndSkipsHistory(t *testing.T) {
	src := &fakeSource{now: t0, rows: []queries.Row{rec(-5*time.Second, 1)}}
	c, path := newCollector(t, src)

	res, err := c.Run()
	if err != nil || !res.Seeded || res.Written != 0 {
		t.Fatalf("seed run: %+v, %v", res, err)
	}
	if len(src.starts) != 0 {
		t.Error("the seed run must not read records")
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil || lines(t, path) != 0 {
		t.Error("the seed run must not write the file")
	}

	// The record 5 s before the seed lies inside the 10 s overlap but predates the seed.
	src.rows = append(src.rows, rec(2*time.Second, 2))
	res, err = c.Run()
	if err != nil || res.Written != 1 {
		t.Fatalf("second run: %+v, %v", res, err)
	}
	if !src.starts[0].Equal(t0) {
		t.Errorf("first real read starts at %v, want the seed %v", src.starts[0], t0)
	}
}

func TestOverlapDedupeAndWatermark(t *testing.T) {
	src := &fakeSource{now: t0}
	c, path := newCollector(t, src)
	if _, err := c.Run(); err != nil {
		t.Fatal(err)
	}

	src.now = t0.Add(time.Minute) // the server clock is ahead of every record below
	src.rows = []queries.Row{rec(time.Second, 1), rec(3*time.Second, 2)}
	if res, _ := c.Run(); res.Written != 2 {
		t.Fatalf("run 2 wrote %d, want 2", res.Written)
	}
	// Run 3: the watermark is t0+3s, so the read starts at t0+3s-10s, clamped to the seed t0.
	if res, _ := c.Run(); res.Written != 0 {
		t.Fatalf("run 3 wrote %d, want 0 (overlap re-read is deduped)", res.Written)
	}
	src.rows = append(src.rows, rec(20*time.Second, 3))
	if res, _ := c.Run(); res.Written != 1 {
		t.Fatalf("run 4 wrote %d, want 1", res.Written)
	}
	// Watermark t0+20s: the read now starts 10 s earlier and re-reads the t0+20s record only.
	if got, want := src.starts[len(src.starts)-1], t0.Add(20*time.Second).Add(-0); got.After(want) {
		t.Errorf("last start %v is after the previous watermark %v", got, want)
	}
	if res, _ := c.Run(); res.Written != 0 {
		t.Fatalf("run 5 wrote %d, want 0", res.Written)
	}
	if got := lines(t, path); got != 3 {
		t.Errorf("file has %d lines, want 3", got)
	}
	if got, want := src.starts[len(src.starts)-1], t0.Add(10*time.Second); !got.Equal(want) {
		t.Errorf("run 5 start = %v, want watermark - overlap = %v", got, want)
	}

	var st state
	if _, err := c.Store.Get(StateKey, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Keys) != 1 {
		t.Errorf("held keys = %d, want only the record inside the overlap window", len(st.Keys))
	}
}

func TestSameTimestampDifferentKeysBothWritten(t *testing.T) {
	src := &fakeSource{now: t0}
	c, path := newCollector(t, src)
	_, _ = c.Run()
	src.rows = []queries.Row{rec(time.Second, 1), rec(time.Second, 2)}
	if res, _ := c.Run(); res.Written != 2 {
		t.Fatalf("wrote %d, want 2 (probe differs)", res.Written)
	}
	if res, _ := c.Run(); res.Written != 0 || lines(t, path) != 2 {
		t.Fatalf("repeat run wrote %d", res.Written)
	}
}

func TestRecordCapFinishesTiesNextRun(t *testing.T) {
	src := &fakeSource{now: t0}
	c, path := newCollector(t, src)
	c.MaxRecords = 2
	_, _ = c.Run()
	// Three records share one timestamp; the cap lands in the middle of the tie.
	src.rows = []queries.Row{rec(time.Second, 1), rec(time.Second, 2), rec(time.Second, 3), rec(2*time.Second, 4)}

	res, _ := c.Run()
	if res.Written != 2 || !res.Capped {
		t.Fatalf("run 1: %+v", res)
	}
	res, _ = c.Run()
	if res.Written != 2 || res.Capped {
		t.Fatalf("run 2: %+v", res)
	}
	if res, _ = c.Run(); res.Written != 0 {
		t.Fatalf("run 3 wrote %d", res.Written)
	}
	if got := lines(t, path); got != 4 {
		t.Errorf("file has %d lines, want 4 with no loss and no repeat", got)
	}
}

func TestZeroRowRunRefreshesState(t *testing.T) {
	defer persist.SetNow(time.Now)
	now := time.Unix(1_000_000, 0)
	persist.SetNow(func() time.Time { return now })

	src := &fakeSource{now: t0}
	c, _ := newCollector(t, src)
	_, _ = c.Run()
	_, saved1 := c.Store.Get(StateKey, new(state))

	now = now.Add(4 * time.Minute)
	if res, err := c.Run(); err != nil || res.Written != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	stamp, err := c.Store.Get(StateKey, new(state))
	if err != nil || stamp != now.Unix() {
		t.Errorf("state timestamp = %d (%v), want it refreshed to %d (first %v)", stamp, err, now.Unix(), saved1)
	}
}

func TestErrorsLeaveStateAlone(t *testing.T) {
	src := &fakeSource{now: t0}
	c, _ := newCollector(t, src)
	_, _ = c.Run()
	src.rows = []queries.Row{rec(time.Second, 1)}
	src.err = errors.New("SQL0551N")
	if _, err := c.Run(); err == nil {
		t.Fatal("want the read error")
	}
	src.err = nil
	if res, _ := c.Run(); res.Written != 1 {
		t.Fatalf("after the error the record must still be collected, wrote %d", res.Written)
	}

	c2, _ := newCollector(t, &fakeSource{nowErr: errors.New("boom")})
	if _, err := c2.Run(); err == nil || !strings.Contains(err.Error(), "server clock") {
		t.Errorf("seed error = %v", err)
	}
	if _, err := c2.Store.Get(StateKey, new(state)); err == nil {
		t.Error("a failed seed must not store state")
	}
}

func TestNoFileWhenNothingToWrite(t *testing.T) {
	src := &fakeSource{now: t0}
	c, path := newCollector(t, src)
	c.Writer.Dir = filepath.Join(c.Writer.Dir, "sub")
	_, _ = c.Run()
	_, _ = c.Run()
	if _, err := os.Stat(c.Writer.Dir); !os.IsNotExist(err) {
		t.Errorf("directory created without records (%s)", path)
	}
}

// A backlog larger than the cap drains over several runs with no loss and no repeat, although every
// read is cut at the limit.
func TestBacklogDrainsWithLimitedReads(t *testing.T) {
	src := &fakeSource{now: t0}
	c, path := newCollector(t, src)
	c.MaxRecords = 2
	_, _ = c.Run()
	for i := 1; i <= 7; i++ {
		src.rows = append(src.rows, rec(time.Duration(i)*time.Second, int64(i)))
	}
	written := 0
	for run := 1; run <= 6; run++ {
		res, err := c.Run()
		if err != nil {
			t.Fatal(err)
		}
		written += res.Written
		if res.Written == 0 {
			break
		}
	}
	if written != 7 || lines(t, path) != 7 {
		t.Errorf("wrote %d records, %d lines, want 7", written, lines(t, path))
	}
	for _, l := range src.limits {
		if l == 0 {
			t.Errorf("limits = %v: a plain backlog must not need the whole-window read", src.limits)
		}
	}
}
