package queries

import (
	"strconv"
	"strings"
	"testing"
)

func TestTopQueries(t *testing.T) {
	for _, limit := range []int{1, 20, 500} {
		q := TopQueries(limit)
		if !strings.HasSuffix(q, " FETCH FIRST "+strconv.Itoa(limit)+" ROWS ONLY") {
			t.Errorf("limit %d is not the row cap: %s", limit, q)
		}
	}
	q := TopQueries(20)
	for _, want := range []string{
		"SELECT MEMBER, HEX(EXECUTABLE_ID) AS EXECUTABLE_ID",
		"MON_GET_PKG_CACHE_STMT(NULL, NULL, NULL, -2)",
		"HEX(EXECUTABLE_ID) AS EXECUTABLE_ID",
		"SUBSTRING(STMT_TEXT, 1, " + strconv.Itoa(TextLimit) + ", CODEUNITS32)",
		// The only exclusion is the integration's own tag, as a prefix match.
		`FULLTXT NOT LIKE '/* nri-db2 */ %'`,
		"ORDER BY TOTAL_ACT_TIME DESC, EXECUTABLE_ID, MEMBER",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("missing %q in %s", want, q)
		}
	}
	if !strings.HasPrefix(q, Tag) {
		t.Errorf("the statement does not start with the tag: %s", q)
	}
	// The tag carries no LIKE wildcard, so the pattern needs no ESCAPE clause.
	if strings.ContainsAny(Tag, "%_") || strings.Contains(q, "ESCAPE") {
		t.Errorf("unexpected wildcard or ESCAPE: %s", q)
	}
}

// The patterns must run on the few ranked entries, not on the whole cache: the inner query ranks
// limit+TopQueriesOverFetch entries with no text predicate, and the outer one filters and trims.
func TestTopQueriesRanksBeforeItFilters(t *testing.T) {
	for _, limit := range []int{1, 20, 500} {
		q := TopQueries(limit)
		inner := " FETCH FIRST " + strconv.Itoa(limit+TopQueriesOverFetch) + " ROWS ONLY) AS T WHERE "
		i := strings.Index(q, inner)
		if i < 0 {
			t.Fatalf("limit %d: no inner cap of %d in %s", limit, limit+TopQueriesOverFetch, q)
		}
		if strings.Contains(q[:i], "LIKE") {
			t.Errorf("limit %d: a LIKE runs before the inner cap: %s", limit, q[:i])
		}
		if strings.Count(q[i:], "FULLTXT NOT LIKE") != 1 {
			t.Errorf("limit %d: want the single tag exclusion on the full text: %s", limit, q[i:])
		}
	}
}

func TestLongRunning(t *testing.T) {
	for _, n := range []int{1, 60, 3600} {
		q := LongRunning(n)
		want := "LOCAL_START_TIME <= CURRENT TIMESTAMP - " + strconv.Itoa(n) + " SECONDS"
		if !strings.Contains(q, want) {
			t.Errorf("threshold %d missing: %s", n, q)
		}
	}
	q := LongRunning(60)
	for _, want := range []string{
		// MEMBER, APPLICATION_HANDLE, UOW_ID and ACTIVITY_ID together identify an activity.
		"SELECT MEMBER, APPLICATION_HANDLE, ACTIVITY_ID, UOW_ID,",
		"MON_GET_ACTIVITY(NULL, -2)",
		"CURRENT TIMESTAMP AS NOW_TS",
		"SUBSTRING(STMT_TEXT, 1, " + strconv.Itoa(TextLimit) + ", CODEUNITS32)",
		"ORDER BY LOCAL_START_TIME",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("missing %q in %s", want, q)
		}
	}
	if !strings.HasSuffix(q, " FETCH FIRST "+strconv.Itoa(ExtrasMaxRows)+" ROWS ONLY") {
		t.Errorf("row cap is not the fixed %d: %s", ExtrasMaxRows, q)
	}
}

func TestLockWaits(t *testing.T) {
	for _, want := range []string{
		// Three parameters: the last one is SYSTEM_APPLS, 0 excludes system applications.
		"MON_GET_APPL_LOCKWAIT(NULL, -2, 0)",
		"CURRENT TIMESTAMP AS NOW_TS",
		"HEX(REQ_EXECUTABLE_ID) AS REQ_EXECUTABLE_ID",
		"ORDER BY LOCK_WAIT_START_TIME",
	} {
		if !strings.Contains(LockWaits, want) {
			t.Errorf("missing %q in %s", want, LockWaits)
		}
	}
	if !strings.HasSuffix(LockWaits, " FETCH FIRST "+strconv.Itoa(ExtrasMaxRows)+" ROWS ONLY") {
		t.Errorf("row cap is not the fixed %d: %s", ExtrasMaxRows, LockWaits)
	}
}

// The extras read table functions, never the snapshot administrative views.
func TestExtrasUseNoSnapshotViews(t *testing.T) {
	for name, q := range map[string]string{
		TopQueriesName: TopQueries(20), LongRunningName: LongRunning(60), LockWaitsName: LockWaits,
	} {
		for _, bad := range []string{"SNAP", "SYSIBMADM.SNAP"} {
			if strings.Contains(strings.ToUpper(q), bad) {
				t.Errorf("%s uses %q: %s", name, bad, q)
			}
		}
		if !strings.Contains(q, "FROM TABLE("+name+"(") {
			t.Errorf("%s is not read through its table function: %s", name, q)
		}
	}
}

// Top SQL hides the integration's own statements by the Tag prefix, so every statement the
// integration sends must carry it.
func TestEveryStatementIsTagged(t *testing.T) {
	for name, q := range map[string]string{
		"Instance": Instance, "Database": Database, "Bufferpool": Bufferpool, "Tablespace": Tablespace,
		"TransactionLog": TransactionLog, "ServerTime": ServerTime, "DiagHist": DiagHist("DIA", "ALL", "", 0),
		"DiagHistLimit": DiagHist("DIA", "ALL", "", 10), "DBMCfg": DBMCfg, "DBCfg": DBCfg, "InstInfo": InstInfo,
		"TopQueries": TopQueries(20), "LongRunning": LongRunning(60), "LockWaits": LockWaits,
	} {
		if !strings.HasPrefix(q, Tag) {
			t.Errorf("%s does not start with the tag: %.60s", name, q)
		}
	}
}
