package queries

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"bytes", []uint8("ACTIVE"), "ACTIVE"},
		{"int32", int32(7), int64(7)},
		{"int64", int64(1) << 40, int64(1) << 40},
		{"float32", float32(1.5), float64(1.5)},
		{"time", now, now},
		{"string", "x", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRowAccessors(t *testing.T) {
	now := time.Now()
	r := Row{"i": int64(3), "f": 2.5, "s": "ACTIVE", "t": now, "n": nil}
	if v, ok := r.Float("i"); !ok || v != 3 {
		t.Errorf("Float(i) = %v, %v", v, ok)
	}
	if v, ok := r.Float("f"); !ok || v != 2.5 {
		t.Errorf("Float(f) = %v, %v", v, ok)
	}
	if _, ok := r.Float("n"); ok {
		t.Error("Float(NULL) must be absent")
	}
	if _, ok := r.Float("s"); ok {
		t.Error("Float(non-numeric string) must be absent")
	}
	if v, ok := (Row{"d": "12.50"}).Float("d"); !ok || v != 12.5 {
		t.Errorf("Float(DECIMAL text) = %v, %v, want 12.5", v, ok)
	}
	if !r.Absent("n") || !r.Absent("missing") || r.Absent("s") {
		t.Error("Absent must be true for NULL and missing only")
	}
	if _, ok := r.Float("missing"); ok {
		t.Error("Float(missing) must be absent")
	}
	if v, ok := r.String("s"); !ok || v != "ACTIVE" {
		t.Errorf("String(s) = %q, %v", v, ok)
	}
	if _, ok := r.String("n"); ok {
		t.Error("String(NULL) must be absent")
	}
	if v, ok := r.Time("t"); !ok || !v.Equal(now) {
		t.Errorf("Time(t) = %v, %v", v, ok)
	}
}

var fakeTS time.Time

func init() { sql.Register("queries-fake", fakeDriver{}) }

// fakeDriver returns one row holding every value type the real driver produces.
type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, io.EOF }

type fakeStmt struct{}

func (s fakeStmt) Close() error  { return nil }
func (s fakeStmt) NumInput() int { return -1 }
func (s fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, io.EOF
}
func (s fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeRows{ts: fakeTS}, nil
}

type fakeRows struct {
	ts   time.Time
	done bool
}

func (r *fakeRows) Columns() []string {
	return []string{"DB_STATUS", "TOTAL_CONS", "ROWS_READ", "CURRENT_TIME", "LAST_BACKUP"}
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = []uint8("ACTIVE")
	dest[1] = int64(5)
	dest[2] = int64(14873)
	dest[3] = r.ts
	dest[4] = nil
	return nil
}

func TestCollect(t *testing.T) {
	ts := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	fakeTS = ts
	db, err := sql.Open("queries-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := Collect(db, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if s, ok := r.String("db_status"); !ok || s != "ACTIVE" {
		t.Errorf("db_status = %q, %v", s, ok)
	}
	if v, ok := r.Float("total_cons"); !ok || v != 5 {
		t.Errorf("total_cons = %v, %v", v, ok)
	}
	if v, ok := r.Float("rows_read"); !ok || v != 14873 {
		t.Errorf("rows_read = %v, %v", v, ok)
	}
	if v, ok := r.Time("current_time"); !ok || !v.Equal(ts) {
		t.Errorf("current_time = %v, %v", v, ok)
	}
	if _, ok := r.Time("last_backup"); ok {
		t.Error("NULL last_backup must be absent")
	}
}
