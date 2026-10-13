package queries

import "strconv"

// Names identify the New Relic extras queries in log lines.
const (
	TopQueriesName  = "MON_GET_PKG_CACHE_STMT"
	LongRunningName = "MON_GET_ACTIVITY"
	LockWaitsName   = "MON_GET_APPL_LOCKWAIT"
)

const (
	// TextLimit is the most characters of statement text that are fetched and reported.
	TextLimit = 4094

	// ExtrasMaxRows caps the long-running and lock-wait queries. It is fixed: the top-SQL query
	// takes its own cap from TOP_QUERIES_LIMIT.
	ExtrasMaxRows = 100
)

// None of the three table functions is granted to PUBLIC, so the monitoring user needs
// GRANT EXECUTE ON FUNCTION SYSPROC.<name> for each collector it enables (SQL0551N / SQLSTATE
// 42501 otherwise), or SYSMON/SQLADM/DBADM.
//
// STMT_TEXT is a CLOB(2M). It is cut to TextLimit characters in SQL, with CODEUNITS32 so a
// multi-byte character is never split, and cast to VARCHAR so the driver does not fetch a LOB.
// EXECUTABLE_ID is VARCHAR(32) FOR BIT DATA, so it is read as HEX.

// The statement ranks the package cache first and applies the exclusion of the integration's own
// statements second. The NOT LIKE predicate reads the whole STMT_TEXT (a CLOB), so with the
// exclusion in the same WHERE as the ranking, Db2 evaluates it on every cache entry and the cost
// grows with the total text size of the cache. Here the inner query takes the top
// limit+topQueriesOverFetch entries by TOTAL_ACT_TIME (no CLOB is read for the ranking) and only
// those few are checked. The integration's own statements all start with Tag, so that prefix is the
// whole exclusion: nothing else is hidden, and the user's custom queries (untagged) are kept. The
// integration's statements are short and fast, so they rank high; the margin covers its 13. If the
// margin is not enough, fewer than limit rows come back. The package cache only exists while the
// database is active (Db2 drops it when the last connection closes and the database deactivates),
// so an idle database that is not activated shows no statements. TOTAL_ACT_TIME is in milliseconds
// and TOTAL_CPU_TIME in microseconds.
const topQueriesInner = Tag + `SELECT MEMBER, HEX(EXECUTABLE_ID) AS EXECUTABLE_ID, SECTION_TYPE, NUM_EXECUTIONS, ` +
	`TOTAL_ACT_TIME, TOTAL_ACT_WAIT_TIME, TOTAL_CPU_TIME, LOCK_WAIT_TIME, ROWS_READ, ROWS_RETURNED, TXT AS STMT_TEXT ` +
	`FROM (SELECT MEMBER, EXECUTABLE_ID, SECTION_TYPE, NUM_EXECUTIONS, TOTAL_ACT_TIME, TOTAL_ACT_WAIT_TIME, ` +
	`TOTAL_CPU_TIME, LOCK_WAIT_TIME, ROWS_READ, ROWS_RETURNED, ` +
	`VARCHAR(SUBSTRING(STMT_TEXT, 1, 4094, CODEUNITS32), 16376) AS TXT, STMT_TEXT AS FULLTXT ` +
	`FROM TABLE(MON_GET_PKG_CACHE_STMT(NULL, NULL, NULL, -2)) ` +
	`ORDER BY TOTAL_ACT_TIME DESC, EXECUTABLE_ID, MEMBER FETCH FIRST `

const topQueriesOuter = ` ROWS ONLY) AS T ` +
	`WHERE FULLTXT NOT LIKE '` + Tag + `%' ` +
	`ORDER BY TOTAL_ACT_TIME DESC, EXECUTABLE_ID, MEMBER FETCH FIRST `

// TopQueriesOverFetch is how many more entries than the limit the inner query ranks, to leave room
// for the statements the outer query removes.
const TopQueriesOverFetch = 30

// TopQueries returns the top-SQL statement, ordered by total activity time. The limit is inlined
// as an integer literal, so the caller must have range-checked it (package config does).
func TopQueries(limit int) string {
	return topQueriesInner + strconv.Itoa(limit+TopQueriesOverFetch) + topQueriesOuter + strconv.Itoa(limit) + " ROWS ONLY"
}

const longRunningHead = Tag + `SELECT MEMBER, APPLICATION_HANDLE, ACTIVITY_ID, UOW_ID, ACTIVITY_STATE, ACTIVITY_TYPE, ` +
	`APPLICATION_NAME, HEX(EXECUTABLE_ID) AS EXECUTABLE_ID, LOCAL_START_TIME, CURRENT TIMESTAMP AS NOW_TS, ` +
	`TOTAL_CPU_TIME, ROWS_READ, ROWS_RETURNED, ` +
	`VARCHAR(SUBSTRING(STMT_TEXT, 1, 4094, CODEUNITS32), 16376) AS STMT_TEXT ` +
	`FROM TABLE(MON_GET_ACTIVITY(NULL, -2)) AS T ` +
	`WHERE LOCAL_START_TIME <= CURRENT TIMESTAMP - `

// LongRunning returns the long-running activity statement. The WHERE clause is a prefilter that
// also drops the monitoring statement itself (it starts after CURRENT TIMESTAMP is taken) and
// queued activities (NULL start time). NOW_TS comes back with each row so the elapsed time is
// computed from one clock. TOTAL_CPU_TIME is in microseconds. The threshold is inlined as an
// integer literal, so the caller must have range-checked it.
func LongRunning(thresholdSeconds int) string {
	return longRunningHead + strconv.Itoa(thresholdSeconds) +
		" SECONDS ORDER BY LOCAL_START_TIME, APPLICATION_HANDLE, UOW_ID, ACTIVITY_ID, MEMBER FETCH FIRST " + strconv.Itoa(ExtrasMaxRows) + " ROWS ONLY"
}

// LockWaits lists the applications now waiting for a lock. MON_GET_APPL_LOCKWAIT has three
// parameters: APPLICATION_HANDLE, MEMBER and SYSTEM_APPLS. The last one, 0, excludes system
// applications. NOW_TS is read in the same statement as LOCK_WAIT_START_TIME.
const LockWaits = Tag + `SELECT REQ_APPLICATION_HANDLE, HLD_APPLICATION_HANDLE, LOCK_NAME, LOCK_OBJECT_TYPE, ` +
	`LOCK_MODE, LOCK_MODE_REQUESTED, HEX(REQ_EXECUTABLE_ID) AS REQ_EXECUTABLE_ID, ` +
	`LOCK_WAIT_START_TIME, CURRENT TIMESTAMP AS NOW_TS ` +
	`FROM TABLE(MON_GET_APPL_LOCKWAIT(NULL, -2, 0)) AS T ` +
	`ORDER BY LOCK_WAIT_START_TIME FETCH FIRST 100 ROWS ONLY`
