package queries

import (
	"fmt"
	"strconv"
)

// DiagName identifies the diagnostic-log query in log lines.
const DiagName = "PD_GET_DIAG_HIST"

// ServerTime reads the Db2 server clock. The diagnostic watermark is seeded from it, because
// PD_GET_DIAG_HIST timestamps are on the server clock.
const ServerTime = Tag + "SELECT CURRENT TIMESTAMP AS server_time FROM SYSIBM.SYSDUMMY1"

// diagHist lists the columns by name. RETCODE is never selected: it corrupts FULLREC with NUL
// bytes (the code is parsed from the FULLREC text instead). TIMEZONE is wrapped into -719..+720
// minutes, so a record from a server at UTC+13 or UTC+14 (or UTC-12) cannot be told from UTC-11 or
// UTC-10 (or UTC+12) by itself; CURRENT TIMEZONE (hhmmss, never wrapped) resolves that.
const diagHist = Tag + "SELECT TIMESTAMP, TIMEZONE, CURRENT TIMEZONE AS SERVER_TZ, RECTYPE, INSTANCENAME, DBPARTITIONNUM, MEMBER, " +
	"LEVEL, IMPACT, DBNAME, EDU_ID, EDUNAME, PID, PROCESS_NAME, TID, " +
	"APPL_ID, APPLHANDLE, AUTH_ID, PRODUCT, COMPONENT, FUNCTION, PROBE, " +
	"CALLEDPRODUCT, CALLEDCOMPONENT, CALLEDFUNCTION, OSERR, MSG, " +
	"HOSTNAME, FULLREC " +
	"FROM TABLE(SYSPROC.PD_GET_DIAG_HIST('%s', '%s', '%s', CAST(? AS TIMESTAMP), NULL, -2)) AS T " +
	"ORDER BY TIMESTAMP"

// DiagHist returns the diagnostic-log statement. It takes one bound parameter, the start time
// (inclusive, microsecond precision). The three arguments are inlined as literals, so the caller
// must have checked them against the allow-lists in package config. A limit above 0 is inlined as
// FETCH FIRST n ROWS ONLY on the TIMESTAMP order; 0 reads every row of the window.
func DiagHist(facility, rectype, impact string, limit int) string {
	q := fmt.Sprintf(diagHist, facility, rectype, impact)
	if limit > 0 {
		q += " FETCH FIRST " + strconv.Itoa(limit) + " ROWS ONLY"
	}
	return q
}
