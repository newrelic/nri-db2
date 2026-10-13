package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLine(t *testing.T, w *Writer, msg string) {
	t.Helper()
	if err := w.Open(); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(map[string]any{"message": msg}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	w := &Writer{Dir: dir, Name: "db2diag-x.jsonl", MaxBytes: 100}
	path := filepath.Join(dir, w.Name)

	writeLine(t, w, "first")
	writeLine(t, w, "second")
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("rotated before the threshold")
	}

	// Pad above the threshold; the next write rotates before appending.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	_, _ = f.WriteString(strings.Repeat("x", 200))
	_ = f.Close()
	writeLine(t, w, "third")

	old, err := os.ReadFile(path + ".1")
	if err != nil || !strings.Contains(string(old), "first") || !strings.Contains(string(old), "xxxx") {
		t.Fatalf(".1 = %q, %v", old, err)
	}
	cur, _ := os.ReadFile(path)
	if strings.TrimSpace(string(cur)) != `{"message":"third"}` {
		t.Errorf("current file = %q", cur)
	}

	// A second rotation replaces the previous .1.
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	_, _ = f.WriteString(strings.Repeat("y", 200))
	_ = f.Close()
	writeLine(t, w, "fourth")
	old, _ = os.ReadFile(path + ".1")
	if strings.Contains(string(old), "first") || !strings.Contains(string(old), "third") {
		t.Errorf(".1 not replaced: %q", old)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, %v", st, err)
	}
}
