// Package queries holds the SQL text and the row-scan helpers. It depends on database/sql only,
// so it is unit-testable without the cgo driver.
package queries

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Tag starts every statement the integration sends. Db2 keeps a leading comment in the package
// cache STMT_TEXT (measured on 12.1.x with the clidriver), so the top-SQL query can tell the
// integration's own statements from the user's by this prefix alone. The user's custom queries
// are not tagged.
const Tag = "/* nri-db2 */ "

// Names identify a query in log lines.
const (
	InstanceName   = "MON_GET_INSTANCE"
	DatabaseName   = "MON_GET_DATABASE"
	BufferpoolName = "MON_GET_BUFFERPOOL"
	TablespaceName = "MON_GET_TABLESPACE"

	TransactionLogName = "MON_GET_TRANSACTION_LOG"
)

// The DB2 queries to execute against a database.
const (
	Instance   = Tag + "SELECT total_connections FROM TABLE(MON_GET_INSTANCE(-1))"
	Database   = Tag + "SELECT appls_cur_cons, appls_in_db2, connections_top, current timestamp AS current_time, db_status, deadlocks, last_backup, lock_list_in_use, lock_timeouts, lock_wait_time, lock_waits, num_locks_held, num_locks_waiting, rows_modified, rows_read, rows_returned, total_cons FROM TABLE(MON_GET_DATABASE(-1))"
	Bufferpool = Tag + "SELECT bp_name, pool_async_col_lbp_pages_found, pool_async_data_lbp_pages_found, pool_async_index_lbp_pages_found, pool_async_xda_lbp_pages_found, pool_col_gbp_l_reads, pool_col_gbp_p_reads, pool_col_l_reads, pool_col_lbp_pages_found, pool_col_p_reads, pool_data_gbp_l_reads, pool_data_gbp_p_reads, pool_data_l_reads, pool_data_lbp_pages_found, pool_data_p_reads, pool_index_gbp_l_reads, pool_index_gbp_p_reads, pool_index_l_reads, pool_index_lbp_pages_found, pool_index_p_reads, pool_temp_col_l_reads, pool_temp_col_p_reads, pool_temp_data_l_reads, pool_temp_data_p_reads, pool_temp_index_l_reads, pool_temp_index_p_reads, pool_temp_xda_l_reads, pool_temp_xda_p_reads, pool_xda_gbp_l_reads, pool_xda_gbp_p_reads, pool_xda_l_reads, pool_xda_lbp_pages_found, pool_xda_p_reads FROM TABLE(MON_GET_BUFFERPOOL(NULL, -1))"
	Tablespace = Tag + "SELECT tbsp_name, tbsp_page_size, tbsp_state, tbsp_total_pages, tbsp_usable_pages, tbsp_used_pages FROM TABLE(MON_GET_TABLESPACE(NULL, -1))"

	TransactionLog = Tag + "SELECT log_reads, log_writes, total_log_available, total_log_used FROM TABLE(MON_GET_TRANSACTION_LOG(-1))"
)

// Querier is satisfied by *sql.DB.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// Row maps a lower-case column name to a normalised value: nil (SQL NULL), string, int64,
// float64 or time.Time.
type Row map[string]any

// Collect runs a query and returns every row with normalised values. The driver returns strings
// as []uint8 and integers as int32 or int64, so values are converted here once.
func Collect(q Querier, query string) ([]Row, error) {
	return CollectArgs(q, query)
}

// CollectArgs is Collect for a statement with bound parameters.
func CollectArgs(q Querier, query string, args ...any) ([]Row, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// The keys are the lower-cased column names; lower-casing them once keeps it out of the row loop.
	keys := make([]string, len(cols))
	for i, c := range cols {
		keys[i] = strings.ToLower(c)
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(Row, len(cols))
		for i, k := range keys {
			row[k] = Normalize(vals[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CustomMaxRows is the most rows of one custom query that are published per run.
const CustomMaxRows = 1000

// CollectLabeled runs a user query and returns the column labels in select order with their
// original case, plus the rows as normalised values in the same order. Collect lower-cases the
// labels and drops the order, which is not enough for custom queries: the label is the metric
// name. Duplicate labels are an error because the second column would silently replace the first.
//
// A maxRows above 0 stops reading after that many rows and reports truncated, so a query that
// matches millions of rows costs neither the memory nor the payload; the cursor is closed with the
// rest unread. 0 reads every row.
func CollectLabeled(q Querier, query string, maxRows int) (cols []string, out [][]any, truncated bool, err error) {
	rows, err := q.Query(query)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = rows.Close() }()

	cols, err = rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	// go_ibm_db returns DECIMAL and DECFLOAT as text ("1.50"), which would be read as an attribute.
	// Measured on 12.1.4: BIGINT int64, SMALLINT int32, REAL and DOUBLE float64, TIMESTAMP time.Time.
	numeric := make([]bool, len(cols))
	if types, err := rows.ColumnTypes(); err == nil {
		for i, ct := range types {
			switch strings.ToUpper(ct.DatabaseTypeName()) {
			case "DECIMAL", "DECFLOAT", "NUMERIC":
				numeric[i] = true
			}
		}
	}
	seen := make(map[string]bool, len(cols))
	for _, c := range cols {
		if seen[c] {
			return nil, nil, false, fmt.Errorf("duplicate column label %q: alias each column", c)
		}
		seen[c] = true
	}
	for rows.Next() {
		if maxRows > 0 && len(out) >= maxRows {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		for i := range vals {
			vals[i] = Normalize(vals[i])
			if s, ok := vals[i].(string); ok && numeric[i] {
				// A value that does not parse (for example DECFLOAT NaN) stays text.
				if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
					vals[i] = f
				}
			}
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return cols, out, truncated, nil
}

// Normalize converts a driver value to nil, string, int64, float64 or time.Time. Other types are
// returned unchanged so a caller can see them.
func Normalize(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(x)
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		return float64(x)
	default:
		return v
	}
}

// Float returns a numeric column as float64. DECIMAL columns arrive as text and are parsed. ok is
// false for NULL, a missing column or a non-numeric value; use Absent to tell those apart.
func (r Row) Float(col string) (float64, bool) {
	switch x := r[col].(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// Absent reports whether the column is missing or NULL, as opposed to present with a type the
// accessor does not accept.
func (r Row) Absent(col string) bool { return r[col] == nil }

// String returns a text column. ok is false for NULL or a missing column.
func (r Row) String(col string) (string, bool) {
	s, ok := r[col].(string)
	return s, ok
}

// Time returns a timestamp column. ok is false for NULL or a missing column.
func (r Row) Time(col string) (time.Time, bool) {
	t, ok := r[col].(time.Time)
	return t, ok
}
