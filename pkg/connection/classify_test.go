package connection

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ibmdb/go_ibm_db"
)

func typed(state string, code int, msg string) error {
	return &go_ibm_db.Error{
		APIName: "SQLDriverConnect",
		Diag:    []go_ibm_db.DiagRecord{{State: state, NativeError: code, Message: msg}},
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Class
	}{
		{"nil", nil, ClassOther},
		{"quiesce", typed("08004", -20157, "[IBM][CLI Driver] SQL20157N  The database is quiesced."), ClassRefused},
		{"unknown database", typed("08004", -30061, "SQL30061N  The database alias was not found."), ClassRefused},
		{"bad password", typed("08001", -30082, `SQL30082N  Security processing failed with reason "24" ("USERNAME AND/OR PASSWORD INVALID").`), ClassAuth},
		{"unreachable or handshake", typed("08001", -30081, "SQL30081N  A communication error has been detected."), ClassConnection},
		{"tls host name", typed("08001", -20576, `SQL20576N  The connection failed. Reason code "1".`), ClassConnection},
		{"killed mid-query 12.1.4", typed("08506", -30108, "SQL30108N  A connection failed but has been re-established."), ClassConnection},
		{"killed mid-query 11.5.9", typed("40003", -30081, "SQL30081N  A communication error ... recv ..."), ClassConnection},
		{"no privilege", typed("42501", -551, "SQL0551N  The authorization ID does not have the required authorization."), ClassOther},
		{"positive native error", typed("40003", 30081, "communication error"), ClassConnection},
		{"sqlcode only, not 08", typed("58004", -30081, "SQL30081N"), ClassConnection},
		{"auth with positive sign", typed("08001", 30082, "bad password"), ClassAuth},
		{"native error 0, code in text", typed("08001", 0, "SQL30082N  Security processing failed"), ClassAuth},
		{"alphanumeric 08S01", typed("08S01", 0, "x"), ClassConnection},
		{"alphanumeric 08S01 in text", errors.New("{08S01} communication"), ClassConnection},
		{"driver bad conn", driver.ErrBadConn, ClassConnection},
		{"wrapped bad conn", fmt.Errorf("wrap: %w", driver.ErrBadConn), ClassConnection},
		{"wrapped typed", fmt.Errorf("ping: %w", typed("08506", -30108, "x")), ClassConnection},
		{"plain error", errors.New("boom"), ClassOther},
		{"message only, refused", errors.New("SQLDriverConnect: {08004} [IBM][CLI Driver] SQL20157N  quiesced"), ClassRefused},
		{"message only, auth", errors.New(`SQLDriverConnect: {08001} [IBM][CLI Driver] SQL30082N  reason "24"`), ClassAuth},
		{"message only, 40003", errors.New("SQLExecute: {40003} [IBM][CLI Driver] SQL30081N  recv"), ClassConnection},
		{"message only, other", errors.New("SQLExecute: {42501} [IBM][CLI Driver][DB2/LINUXX8664] SQL0551N  no auth"), ClassOther},
		{"message only, 55032 state alone", errors.New("SQLExecute: {55032} agent error"), ClassConnection},
		{"message only, SQL1224N alone", errors.New("SQLExecute: {58004} SQL1224N  agent not available"), ClassConnection},
		{"typed SQL1229N alone", typed("58004", -1229, "system error"), ClassConnection},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Errorf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassPredicates(t *testing.T) {
	tests := []struct {
		c         Class
		retryable bool
		failure   bool
	}{
		{ClassConnection, true, true},
		{ClassAuth, false, true},
		{ClassRefused, false, true},
		{ClassOther, false, false},
	}
	for _, tt := range tests {
		if got := tt.c.Retryable(); got != tt.retryable {
			t.Errorf("%v.Retryable() = %v, want %v", tt.c, got, tt.retryable)
		}
		if got := tt.c.IsConnectionFailure(); got != tt.failure {
			t.Errorf("%v.IsConnectionFailure() = %v, want %v", tt.c, got, tt.failure)
		}
	}
}

func TestReason(t *testing.T) {
	err := typed("08001", -30082, `SQL30082N  Security processing failed with reason "24"; password=Secret1`)
	got := Reason(err)
	if want := "authentication (SQLSTATE 08001, SQL30082N)"; got != want {
		t.Errorf("Reason() = %q, want %q", got, want)
	}
	if got := Reason(nil); got != "" {
		t.Errorf("Reason(nil) = %q, want empty", got)
	}
}

func TestReasonDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"plain", errors.New("boom"), "other: boom"},
		{"duplicate column label", fmt.Errorf("duplicate column label %q: alias each column", "tbsp_name"),
			`other: duplicate column label "tbsp_name": alias each column`},
		{"bad conn", fmt.Errorf("no connection: %w", driver.ErrBadConn), "connection: no connection: driver: bad connection"},
		{"whitespace collapsed", errors.New("scan\n\t failed   on  column"), "other: scan failed on column"},
		{"password redacted", errors.New("open failed: DATABASE=X;UID=monuser;PWD=Secret1;HOSTNAME=h"),
			"other: open failed: DATABASE=X;UID=***;PWD=***;HOSTNAME=h"},
		{"lower case and spaced", errors.New("password=Secret1 rejected"), "other: password=*** rejected"},
		{"typed without diag records", &go_ibm_db.Error{APIName: "SQLDriverConnect"}, "other"},
		{"typed with secret in message, no state", typed("", 0, "PWD=Secret1"), "other (SQLSTATE )"},
		{"wrapped typed without diag records", fmt.Errorf("ping: %w", &go_ibm_db.Error{}), "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Reason(tt.err)
			if got != tt.want {
				t.Errorf("Reason() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "Secret1") {
				t.Errorf("Reason() leaks the password: %q", got)
			}
		})
	}
}

func TestReasonTruncates(t *testing.T) {
	got := Reason(errors.New(strings.Repeat("é", 500)))
	want := "other: " + strings.Repeat("é", maxDetail) + "..."
	if got != want {
		t.Errorf("Reason() = %d bytes, want %d bytes (cut on a rune boundary)", len(got), len(want))
	}
	if !utf8.ValidString(got) {
		t.Errorf("Reason() is not valid UTF-8: %q", got)
	}
	short := strings.Repeat("x", maxDetail)
	if got := Reason(errors.New(short)); got != "other: "+short {
		t.Errorf("an error of exactly %d characters was changed: %q", maxDetail, got)
	}
}
