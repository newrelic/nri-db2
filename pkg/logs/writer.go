package logs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	dirMode  = 0o750
	fileMode = 0o640
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// FileName is db2diag-<host>_<port>_<db>.jsonl with every character outside [A-Za-z0-9._-]
// replaced by '_'.
func FileName(id Identity) string {
	name := fmt.Sprintf("db2diag-%s_%s_%s.jsonl", id.Hostname, id.Port, id.Database)
	return unsafeName.ReplaceAllString(name, "_")
}

// Writer appends JSON lines to one file and rotates it by size.
type Writer struct {
	Dir      string
	Name     string
	MaxBytes int64

	f *os.File
	w *bufio.Writer
}

// Open creates the directory, rotates the file when it has reached MaxBytes and opens it for
// append. The file is rotated before appending, so at most two generations exist: <name> and
// <name>.1, which replaces any earlier .1.
func (w *Writer) Open() error {
	if err := os.MkdirAll(w.Dir, dirMode); err != nil {
		return err
	}
	path := filepath.Join(w.Dir, w.Name)
	if st, err := os.Stat(path); err == nil && st.Size() >= w.MaxBytes {
		if err := rotate(path); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode) //nolint:gosec // operator-chosen directory
	if err != nil {
		return err
	}
	w.f, w.w = f, bufio.NewWriter(f)
	return nil
}

// Write appends one record as one line.
func (w *Writer) Write(rec map[string]any) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = w.w.Write(append(b, '\n'))
	return err
}

// Close flushes and closes the file. The collector updates the stored state only after it
// returns nil, so a crash repeats records instead of losing them.
func (w *Writer) Close() error {
	if w.f == nil {
		return nil
	}
	err := w.w.Flush()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	w.f, w.w = nil, nil
	return err
}
