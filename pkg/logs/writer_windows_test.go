//go:build windows

package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file held open by another process cannot be renamed on Windows. Open keeps appending to it
// and leaves no .1 behind.
func TestOpenAppendsWhenRotationIsBlocked(t *testing.T) {
	dir := t.TempDir()
	w := &Writer{Dir: dir, Name: "d.jsonl", MaxBytes: 100}
	path := filepath.Join(dir, "d.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 200)), 0o640); err != nil {
		t.Fatal(err)
	}
	// os.Open does not set FILE_SHARE_DELETE, so renaming the file fails while it is open.
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()

	writeLine(t, w, "after")
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error(".1 exists although the rename was blocked")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "after") {
		t.Errorf("record not appended: %q", b)
	}
}

// Only a sharing violation is tolerated. A rename refused for another reason will not clear by
// itself, so Open returns it. A read-only .1 file makes the replace fail with access denied.
func TestOpenReturnsRotationAccessDenied(t *testing.T) {
	dir := t.TempDir()
	w := &Writer{Dir: dir, Name: "d.jsonl", MaxBytes: 100}
	path := filepath.Join(dir, "d.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 200)), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("old"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := w.Open(); err == nil {
		_ = w.Close()
		t.Error("Open succeeded although the rotation was refused")
	}
}
