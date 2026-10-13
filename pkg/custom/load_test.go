package custom

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func collectWarn() (Warn, *[]string) {
	var msgs []string
	return func(format string, args ...any) { msgs = append(msgs, fmt.Sprintf(format, args...)) }, &msgs
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "q.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFileDefaults(t *testing.T) {
	p := writeYAML(t, `
queries:
  - query: SELECT 1 AS "a" FROM SYSIBM.SYSDUMMY1;
  - name: pools
    query: >-
      select bp_name AS "bufferpoolName", pool_data_l_reads AS "dataLogicalReads"
      from table(mon_get_bufferpool(null,-1))
    metric_types:
      dataLogicalReads: GAUGE
      bufferpoolName: attribute
    sample_name: MyCustomSample
`)
	warn, msgs := collectWarn()
	qs := Load("", p, warn)
	if len(*msgs) != 0 {
		t.Fatalf("unexpected warnings: %v", *msgs)
	}
	if len(qs) != 2 {
		t.Fatalf("queries = %d, want 2", len(qs))
	}
	if qs[0].Name != "custom1" || qs[0].Sample != DefaultSample || qs[0].SQL != `SELECT 1 AS "a" FROM SYSIBM.SYSDUMMY1` {
		t.Errorf("defaults wrong: %+v", qs[0])
	}
	if qs[1].Name != "pools" || qs[1].Sample != "MyCustomSample" ||
		qs[1].Types["dataLogicalReads"] != Gauge || qs[1].Types["bufferpoolName"] != Attribute {
		t.Errorf("explicit values wrong: %+v", qs[1])
	}
}

func TestLoadInlineAndFileBothRun(t *testing.T) {
	p := writeYAML(t, "queries:\n  - name: f\n    query: SELECT 2 AS \"b\" FROM SYSIBM.SYSDUMMY1\n")
	warn, msgs := collectWarn()
	qs := Load(`SELECT 1 AS "a" FROM SYSIBM.SYSDUMMY1`, p, warn)
	if len(*msgs) != 0 || len(qs) != 2 || qs[0].Name != InlineName || qs[1].Name != "f" {
		t.Fatalf("qs = %+v, warnings = %v", qs, *msgs)
	}
}

func TestLoadRejections(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"pdelta", "queries:\n  - name: q\n    query: SELECT 1 AS c FROM t\n    metric_types:\n      c: pdelta\n", `column "c": unknown metric type "pdelta"`},
		{"prate", "queries:\n  - name: q\n    query: SELECT 1 AS c FROM t\n    metric_types:\n      c: prate\n", `unknown metric type "prate"`},
		{"unknown", "queries:\n  - name: q\n    query: SELECT 1 AS c FROM t\n    metric_types:\n      c: nonsense\n", `unknown metric type "nonsense"`},
		{"delete", "queries:\n  - name: q\n    query: DELETE FROM t\n", "only SELECT, WITH and VALUES"},
		{"call", "queries:\n  - name: q\n    query: CALL SYSPROC.ADMIN_CMD('x')\n", "only SELECT, WITH and VALUES"},
		{"chained", "queries:\n  - name: q\n    query: SELECT 1 FROM t; DELETE FROM t\n", "only one statement"},
		{"empty", "queries:\n  - name: q\n    query: ''\n", "empty query"},
		{"only paren", "queries:\n  - name: q\n    query: '('\n", "only SELECT, WITH and VALUES"},
		{"final table", "queries:\n  - name: q\n    query: SELECT * FROM FINAL TABLE (INSERT INTO t VALUES (1))\n", "FINAL TABLE"},
		{"new table lower case", "queries:\n  - name: q\n    query: select * from new table (insert into t values (1))\n", "NEW TABLE"},
		{"old table, line break", "queries:\n  - name: q\n    query: \"SELECT * FROM old\\n\\tTable (DELETE FROM t)\"\n", "OLD TABLE"},
		{"final table, comment between", "queries:\n  - name: q\n    query: \"SELECT * FROM FINAL /* x */ TABLE (UPDATE t SET a = 1)\"\n", "FINAL TABLE"},
		{"final table after a literal", "queries:\n  - name: q\n    query: \"SELECT 'it''s' FROM FINAL TABLE (INSERT INTO t VALUES (1))\"\n", "FINAL TABLE"},
		{"duplicate", "queries:\n  - name: q\n    query: SELECT 1 FROM t\n  - name: q\n    query: SELECT 2 FROM t\n", "duplicate query name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warn, msgs := collectWarn()
			qs := Load("", writeYAML(t, tt.yaml), warn)
			if len(*msgs) != 1 || !strings.Contains((*msgs)[0], tt.want) || !strings.Contains((*msgs)[0], `"q"`) {
				t.Errorf("warnings = %v, want one naming %q and the query", *msgs, tt.want)
			}
			wantKept := 0
			if tt.name == "duplicate" {
				wantKept = 1
			}
			if len(qs) != wantKept {
				t.Errorf("kept %d queries, want %d", len(qs), wantKept)
			}
		})
	}
}

func TestLoadAcceptsLeadingParenAndWith(t *testing.T) {
	warn, msgs := collectWarn()
	p := writeYAML(t, "queries:\n  - name: a\n    query: (SELECT 1 FROM t)\n  - name: b\n    query: \"  with r(n) as (values 1) select n from r\"\n  - name: c\n    query: \"VALUES (1, ';')\"\n")
	if qs := Load("", p, warn); len(qs) != 3 || len(*msgs) != 0 {
		t.Errorf("qs = %d, warnings = %v", len(qs), *msgs)
	}
}

func TestLoadAcceptsDataChangeWordsOutsideSyntax(t *testing.T) {
	queries := []string{
		`SELECT 'FINAL TABLE' AS "a" FROM SYSIBM.SYSDUMMY1`,
		`SELECT 'it''s a new table' AS "a" FROM SYSIBM.SYSDUMMY1`,
		`SELECT 1 AS "final table" FROM SYSIBM.SYSDUMMY1`,
		`SELECT "OLD" AS "new", 1 FROM "FINAL" TABLE_X`,
		"SELECT 1 FROM SYSIBM.SYSDUMMY1 -- final table\n",
		`SELECT 1 /* old table */ FROM SYSIBM.SYSDUMMY1`,
		`SELECT 1 FROM t AS old, TABLE(MON_GET_TABLESPACE(NULL, -1)) AS m`,
		`SELECT final_table, new_tables FROM t`,
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			warn, msgs := collectWarn()
			if qs := Load(q, "", warn); len(qs) != 1 || len(*msgs) != 0 {
				t.Errorf("qs = %d, warnings = %v, want the query accepted", len(qs), *msgs)
			}
		})
	}
}

func TestLoadFileProblemsKeepInline(t *testing.T) {
	tests := map[string]string{
		"missing":     filepath.Join(t.TempDir(), "nope.yml"),
		"bad yaml":    writeYAML(t, "queries: [\n"),
		"unknown key": writeYAML(t, "queries:\n  - name: q\n    querry: SELECT 1\n"),
	}
	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			warn, msgs := collectWarn()
			qs := Load("SELECT 1 FROM t", path, warn)
			if len(qs) != 1 || qs[0].Name != InlineName {
				t.Errorf("qs = %+v, want only the inline query", qs)
			}
			if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "CUSTOM_METRICS_CONFIG") {
				t.Errorf("warnings = %v", *msgs)
			}
		})
	}
}

func TestLoadNothingConfigured(t *testing.T) {
	warn, _ := collectWarn()
	if qs := Load("  ", "", warn); len(qs) != 0 {
		t.Errorf("qs = %+v", qs)
	}
}
