package queries

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"testing"
)

func init() { sql.Register("queries-labeled", labeledDriver{}) }

// labeledDriver returns two rows; the SQL text picks the column labels.
type labeledDriver struct{}

func (labeledDriver) Open(string) (driver.Conn, error) { return labeledConn{}, nil }

type labeledConn struct{}

func (labeledConn) Prepare(q string) (driver.Stmt, error) { return labeledStmt{q}, nil }
func (labeledConn) Close() error                          { return nil }
func (labeledConn) Begin() (driver.Tx, error)             { return nil, io.EOF }

type labeledStmt struct{ q string }

func (s labeledStmt) Close() error                               { return nil }
func (s labeledStmt) NumInput() int                              { return -1 }
func (s labeledStmt) Exec([]driver.Value) (driver.Result, error) { return nil, io.EOF }
func (s labeledStmt) Query([]driver.Value) (driver.Rows, error) {
	if strings.Contains(s.q, "dup") {
		return &labeledRows{cols: []string{"A", "A"}}, nil
	}
	if strings.Contains(s.q, "dec") {
		return &labeledRows{cols: []string{"D", "S", "N"}}, nil
	}
	if strings.Contains(s.q, "many") {
		return &labeledRows{cols: []string{"name", "n"}, total: 10}, nil
	}
	return &labeledRows{cols: []string{"bufferpoolName", "ROWS", "n"}}, nil
}

type labeledRows struct {
	cols  []string
	i     int
	total int
}

func (r *labeledRows) Columns() []string { return r.cols }

// ColumnTypeDatabaseTypeName marks column D as DECIMAL and N as DECFLOAT, as go_ibm_db reports them.
func (r *labeledRows) ColumnTypeDatabaseTypeName(i int) string {
	if r.cols[0] != "D" {
		return ""
	}
	return []string{"DECIMAL", "VARCHAR", "DECFLOAT"}[i]
}
func (r *labeledRows) Close() error { return nil }
func (r *labeledRows) Next(dest []driver.Value) error {
	total := r.total
	if total == 0 {
		total = 2
	}
	if r.i >= total {
		return io.EOF
	}
	r.i++
	switch {
	case r.total > 0:
		dest[0], dest[1] = []uint8("pool"), int64(r.i)
	case r.cols[0] == "D":
		dest[0], dest[1], dest[2] = []uint8("1.50"), []uint8("1.50"), []uint8("NaN")
	case len(dest) == 3:
		dest[0] = []uint8("IBMDEFAULTBP")
		dest[1] = int64(r.i)
		dest[2] = nil
	default:
		dest[0], dest[1] = int64(1), int64(2)
	}
	return nil
}

func TestCollectLabeled(t *testing.T) {
	db, err := sql.Open("queries-labeled", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cols, rows, truncated, err := CollectLabeled(db, "SELECT 1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cols, ","); got != "bufferpoolName,ROWS,n" {
		t.Errorf("cols = %s, want original case and order", got)
	}
	if len(rows) != 2 || truncated {
		t.Fatalf("rows = %d truncated = %t, want 2 and false", len(rows), truncated)
	}
	if rows[0][0] != "IBMDEFAULTBP" || rows[1][1] != int64(2) || rows[0][2] != nil {
		t.Errorf("rows = %#v, want normalised values in column order", rows)
	}

	if _, _, _, err := CollectLabeled(db, "SELECT dup", 0); err == nil || !strings.Contains(err.Error(), `duplicate column label "A"`) {
		t.Errorf("duplicate labels err = %v", err)
	}
}

func TestCollectLabeledDecimalIsNumber(t *testing.T) {
	db, err := sql.Open("queries-labeled", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	_, rows, _, err := CollectLabeled(db, "SELECT dec", 0)
	if err != nil {
		t.Fatal(err)
	}
	// DECIMAL text becomes a number; VARCHAR text that looks numeric stays text; DECFLOAT NaN stays text.
	if rows[0][0] != 1.5 || rows[0][1] != "1.50" || rows[0][2] != "NaN" {
		t.Errorf("row = %#v", rows[0])
	}
}

func TestCollectLabeledRowCap(t *testing.T) {
	db, err := sql.Open("queries-labeled", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	tests := []struct {
		name          string
		maxRows       int
		wantRows      int
		wantTruncated bool
	}{
		{"cap below the result", 3, 3, true},
		{"cap one below the result", 9, 9, true},
		{"cap equal to the result is not a truncation", 10, 10, false},
		{"cap above the result", 11, 10, false},
		{"zero reads everything", 0, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, rows, truncated, err := CollectLabeled(db, "SELECT many", tt.maxRows)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tt.wantRows || truncated != tt.wantTruncated {
				t.Errorf("rows = %d truncated = %t, want %d and %t", len(rows), truncated, tt.wantRows, tt.wantTruncated)
			}
		})
	}
}

// Collect keys every row by the lower-cased column name, whatever case Db2 reports.
func TestCollectLowerCasesColumnNames(t *testing.T) {
	db, err := sql.Open("queries-labeled", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := Collect(db, "SELECT 1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d, err = %v, want 2 rows", len(rows), err)
	}
	for _, r := range rows {
		for _, k := range []string{"bufferpoolname", "rows", "n"} {
			if _, ok := r[k]; !ok {
				t.Errorf("row %v has no key %q", r, k)
			}
		}
		if _, ok := r["bufferpoolName"]; ok {
			t.Errorf("row %v kept the original case", r)
		}
	}
}
