package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/config"
	"github.com/newrelic/nri-db2/pkg/logs"
	"github.com/newrelic/nri-db2/pkg/queries"
)

type stubSource struct {
	now      time.Time
	rows     []queries.Row
	err      error
	nowCalls int
}

func (s *stubSource) ServerNow() (time.Time, error) { s.nowCalls++; return s.now, nil }
func (s *stubSource) Rows(time.Time, int) ([]queries.Row, error) {
	return s.rows, s.err
}

func diagConfig(dir string) config.Config {
	return config.Config{
		Hostname: "db2-121", Port: 50000, Database: "testdb", CollectDiagLogs: true, DiagLogDir: dir,
		DiagOverlapSeconds: 10, DiagMaxRecordsPerRun: 100, DiagMaxFileMB: 1,
	}
}

func TestCollectDiagSeedsThenWrites(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	src := &stubSource{now: t0}
	store := persist.NewInMemoryStore()
	cfg := diagConfig(dir)

	collectDiag(src, store, cfg)
	if src.nowCalls != 1 {
		t.Fatalf("seed run asked for the server clock %d times", src.nowCalls)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("seed run wrote %d files", len(entries))
	}

	src.rows = []queries.Row{{
		"timestamp": t0.Add(time.Second), "pid": int64(1), "tid": int64(2), "rectype": "E", "function": "f", "probe": int64(3),
		"fullrec": "2026-10-09-15.00.01.000000+000 I1E1   LEVEL: Error\n",
	}}
	collectDiag(src, store, cfg)
	b, err := os.ReadFile(filepath.Join(dir, "db2diag-db2-121_50000_TESTDB.jsonl"))
	if err != nil || len(b) == 0 {
		t.Fatalf("no output: %v", err)
	}
}

// A privilege error is a warning: nothing is written, the stored state is untouched, no panic.
func TestCollectDiagErrorsAreWarnings(t *testing.T) {
	for _, err := range []error{dbErr("42501", 551), dbErr("08001", 30081), errors.New("boom")} {
		dir := t.TempDir()
		src := &stubSource{now: time.Now(), err: err}
		store := persist.NewInMemoryStore()
		cfg := diagConfig(dir)

		collectDiag(src, store, cfg) // seed
		collectDiag(src, store, cfg) // read fails
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%v: wrote %d files", err, len(entries))
		}
	}
}

func TestCollectDiagNeedsStore(t *testing.T) {
	src := &stubSource{now: time.Now()}
	collectDiag(src, nil, diagConfig(t.TempDir()))
	if src.nowCalls != 0 {
		t.Error("collected without a state store")
	}
}

func TestCollectDiagUnwritableDirIsWarning(t *testing.T) {
	src := &stubSource{now: time.Unix(0, 0).UTC()}
	store := persist.NewInMemoryStore()
	unwritable := "/dev/null/sub"
	if runtime.GOOS == "windows" {
		// /dev/null/sub is creatable on Windows; a directory below a regular file is not.
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		unwritable = filepath.Join(file, "sub")
	}
	cfg := diagConfig(unwritable)
	collectDiag(src, store, cfg) // seed
	src.rows = []queries.Row{{"timestamp": time.Unix(5, 0).UTC(), "fullrec": "x"}}
	collectDiag(src, store, cfg) // Open fails, must only warn
	if wm, err := logs.Watermark(store); err != nil || !wm.Equal(time.Unix(0, 0).UTC()) {
		t.Errorf("watermark moved after a write failure: %v %v", wm, err)
	}
}

// An unusable DIAG_LOG_DIR turns diag off for the run instead of ending it with exitInvalidConfig.
func TestDiagDirUsable(t *testing.T) {
	good := config.Config{CollectDiagLogs: true, DiagLogDir: filepath.Join(t.TempDir(), "sub")}
	if !diagDirUsable(good) {
		t.Error("a creatable directory was reported unusable")
	}

	// A directory below a regular file cannot be created, even by root.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := config.Config{CollectDiagLogs: true, DiagLogDir: filepath.Join(file, "sub")}
	if diagDirUsable(bad) {
		t.Error("an uncreatable directory was reported usable")
	}
}
