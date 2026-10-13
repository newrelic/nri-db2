package queries

import (
	"strings"
	"testing"
)

func TestDiagHist(t *testing.T) {
	q := DiagHist("MAIN", "E+D", "", 0)
	if strings.Contains(strings.ToUpper(q), "RETCODE") {
		t.Fatalf("RETCODE must never be selected: %s", q)
	}
	if n := strings.Count(q, "?"); n != 1 {
		t.Fatalf("want exactly one bound parameter, got %d", n)
	}
	for _, want := range []string{
		"PD_GET_DIAG_HIST('MAIN', 'E+D', '', CAST(? AS TIMESTAMP), NULL, -2)",
		"FULLREC",
		"CURRENT TIMEZONE AS SERVER_TZ",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("missing %q in %s", want, q)
		}
	}
	if !strings.HasSuffix(q, "ORDER BY TIMESTAMP") {
		t.Errorf("rows must be ordered by TIMESTAMP: %s", q)
	}
	if strings.Contains(q, "%") {
		t.Errorf("unformatted verb left in %s", q)
	}
}

func TestDiagHistLimit(t *testing.T) {
	q := DiagHist("MAIN", "E+D", "", 250)
	if !strings.HasSuffix(q, "ORDER BY TIMESTAMP FETCH FIRST 250 ROWS ONLY") {
		t.Errorf("the cap must follow the TIMESTAMP order: %s", q)
	}
	if n := strings.Count(q, "?"); n != 1 {
		t.Errorf("want exactly one bound parameter, got %d", n)
	}
	for _, limit := range []int{0, -1} {
		if got := DiagHist("MAIN", "E+D", "", limit); strings.Contains(got, "FETCH FIRST") {
			t.Errorf("limit %d must read the whole window: %s", limit, got)
		}
	}
}
