package connection

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ibmdb/go_ibm_db"
)

// Class is the category of a Db2 error that matters to the connect flow.
type Class int

const (
	// ClassOther is any error unrelated to the connection (for example SQL0551N).
	ClassOther Class = iota
	// ClassConnection is a lost or failed connection that a reconnect may fix.
	ClassConnection
	// ClassAuth is a failed login (SQL30082N). It also carries SQLSTATE 08001 but is never retried.
	ClassAuth
	// ClassRefused is a server that answered and refused: SQLSTATE 08004 (SQL30061N unknown
	// database, SQL1060N no CONNECT privilege, SQL20157N quiesce). A retry cannot succeed.
	ClassRefused
)

func (c Class) String() string {
	switch c {
	case ClassConnection:
		return "connection"
	case ClassAuth:
		return "authentication"
	case ClassRefused:
		return "refused"
	default:
		return "other"
	}
}

// IsConnectionFailure reports whether the error means the session is unusable, so canConnect
// must be 0. Authentication and refused errors are connection failures that are not retryable.
func (c Class) IsConnectionFailure() bool { return c != ClassOther }

// Retryable reports whether one reconnect may help.
func (c Class) Retryable() bool { return c == ClassConnection }

const (
	sqlcodeAuth          = 30082 // SQL30082N: security processing failed
	sqlstateRefused      = "08004"
	sqlstateConnClassPre = "08"
	sqlstateLostConn     = "40003"
	sqlstateReconnect    = "55032"
)

// Connection-class SQLCODEs regardless of SQLSTATE: 30081 communication error, 30108 connection
// failed in an automatic client reroute environment, 1224 and 1229 database agent/system errors.
// 1224 and 1229 were not produced in the spikes, so they are untested.
var connectionSQLCodes = map[int]bool{30081: true, 30108: true, 1224: true, 1229: true}

// Matches the text of a diagnostic record: "{08004} ... SQL20157N ...".
var (
	stateRe   = regexp.MustCompile(`\{([0-9A-Za-z]{5})\}`)
	sqlcodeRe = regexp.MustCompile(`SQL(\d+)N`)
)

// diag is one SQLSTATE/SQLCODE pair taken from an error.
type diag struct {
	state string
	code  int // absolute value of the SQLCODE; 0 when unknown
}

// Classify categorizes an error from the driver. The SQLSTATE and SQLCODE come from the typed
// fields of *go_ibm_db.Error (Diag[].State, Diag[].NativeError), with the message text as the
// fallback for other error types and for records whose NativeError is 0.
func Classify(err error) Class {
	if err == nil {
		return ClassOther
	}
	if errors.Is(err, driver.ErrBadConn) {
		return ClassConnection
	}

	diags := extractDiags(err)
	// Authentication first: SQL30082N also carries SQLSTATE 08001.
	for _, d := range diags {
		if d.code == sqlcodeAuth {
			return ClassAuth
		}
	}
	for _, d := range diags {
		if d.state == sqlstateRefused {
			return ClassRefused
		}
	}
	for _, d := range diags {
		if strings.HasPrefix(d.state, sqlstateConnClassPre) ||
			d.state == sqlstateLostConn || d.state == sqlstateReconnect ||
			connectionSQLCodes[d.code] {
			return ClassConnection
		}
	}
	return ClassOther
}

// Reason returns a short description for logs: class plus SQLSTATE and SQLCODE of the first
// diagnostic record. An error from the driver contributes only those codes, because its message
// can echo the connection string. Any other error without a diagnostic record (a duplicate column
// label, a scan failure) adds its sanitized text, so the log says what went wrong.
func Reason(err error) string {
	if err == nil {
		return ""
	}
	c := Classify(err)
	diags := extractDiags(err)
	if len(diags) == 0 {
		var dbErr *go_ibm_db.Error
		if errors.As(err, &dbErr) {
			return c.String()
		}
		return c.String() + ": " + sanitize(err.Error())
	}
	d := diags[0]
	if d.code == 0 {
		return fmt.Sprintf("%s (SQLSTATE %s)", c, d.state)
	}
	return fmt.Sprintf("%s (SQLSTATE %s, SQL%dN)", c, d.state, d.code)
}

// maxDetail caps the error text in Reason, in characters.
const maxDetail = 200

// secretRe matches the credential keywords of a connection string with their values.
var secretRe = regexp.MustCompile(`(?i)(pwd|password|uid)=[^;\s]*`)

// sanitize redacts credential values, collapses whitespace and cuts the text to maxDetail
// characters on a rune boundary.
func sanitize(msg string) string {
	msg = secretRe.ReplaceAllString(msg, "${1}=***")
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > maxDetail {
		msg = string(r[:maxDetail]) + "..."
	}
	return msg
}

func extractDiags(err error) []diag {
	var dbErr *go_ibm_db.Error
	if errors.As(err, &dbErr) && len(dbErr.Diag) > 0 {
		out := make([]diag, 0, len(dbErr.Diag))
		for _, r := range dbErr.Diag {
			d := diag{state: r.State, code: abs(r.NativeError)}
			if d.code == 0 {
				d.code = sqlcodeFromText(r.Message)
			}
			out = append(out, d)
		}
		return out
	}
	return parseText(err.Error())
}

// parseText reads "{STATE} ... SQLnnnnnN" pairs from a message. Each state starts a record; the
// first SQL code that follows belongs to it.
func parseText(msg string) []diag {
	states := stateRe.FindAllStringSubmatchIndex(msg, -1)
	if len(states) == 0 {
		if code := sqlcodeFromText(msg); code != 0 {
			return []diag{{code: code}}
		}
		return nil
	}
	out := make([]diag, 0, len(states))
	for i, s := range states {
		end := len(msg)
		if i+1 < len(states) {
			end = states[i+1][0]
		}
		out = append(out, diag{
			state: msg[s[2]:s[3]],
			code:  sqlcodeFromText(msg[s[1]:end]),
		})
	}
	return out
}

func sqlcodeFromText(s string) int {
	m := sqlcodeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
