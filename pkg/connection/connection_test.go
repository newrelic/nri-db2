package connection

import (
	"database/sql"
	"errors"
	"testing"
)

func fakeEnv(initial map[string]string) (func(string) (string, bool), func(string, string) error, map[string]string) {
	env := map[string]string{}
	for k, v := range initial {
		env[k] = v
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	set := func(k, v string) error { env[k] = v; return nil }
	return lookup, set, env
}

func TestEnsureConnRetriesEnv(t *testing.T) {
	tests := []struct {
		name    string
		initial map[string]string
		want    string
	}{
		{"unset gives 0", nil, "0"},
		{"preset value is kept", map[string]string{connRetriesEnv: "1"}, "1"},
		{"empty but set is kept", map[string]string{connRetriesEnv: ""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup, set, env := fakeEnv(tt.initial)
			if err := ensureConnRetriesEnv(lookup, set); err != nil {
				t.Fatal(err)
			}
			if got := env[connRetriesEnv]; got != tt.want {
				t.Errorf("%s = %q, want %q", connRetriesEnv, got, tt.want)
			}
		})
	}
}

func TestEnsureConnRetriesEnvPropagatesSetError(t *testing.T) {
	lookup, _, _ := fakeEnv(nil)
	boom := errors.New("boom")
	if err := ensureConnRetriesEnv(lookup, func(string, string) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestConnectWithBudget(t *testing.T) {
	connErr := typed("08001", -30081, "SQL30081N")
	tests := []struct {
		name      string
		errs      []error // result of each successive open
		wantOpens int
		wantErr   bool
		wantSpent bool // the reconnect budget is spent after Connect
	}{
		{"first open works", []error{nil}, 1, false, false},
		{"connection error retried once, then works", []error{connErr, nil}, 2, false, true},
		{"connection error retried once, then fails", []error{connErr, connErr}, 2, true, false},
		{"auth is not retried", []error{typed("08001", -30082, "SQL30082N")}, 1, true, false},
		{"refused is not retried", []error{typed("08004", -20157, "SQL20157N")}, 1, true, false},
		{"other is not retried", []error{typed("42501", -551, "SQL0551N")}, 1, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opens := 0
			open := func(DSN) (*sql.DB, error) {
				err := tt.errs[opens]
				opens++
				if err != nil {
					return nil, err
				}
				return sql.OpenDB(stubConnector{}), nil
			}
			s, err := connectWith(DSN{}, open)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
			if opens != tt.wantOpens {
				t.Errorf("opens = %d, want %d", opens, tt.wantOpens)
			}
			if s != nil && s.reconnected != tt.wantSpent {
				t.Errorf("budget spent = %t, want %t", s.reconnected, tt.wantSpent)
			}
		})
	}
}

// A connect that needed its retry leaves no budget for a mid-run reconnect.
func TestConnectRetryLeavesNoMidRunReconnect(t *testing.T) {
	connErr := typed("08001", -30081, "SQL30081N")
	opens := 0
	open := func(DSN) (*sql.DB, error) {
		opens++
		if opens == 1 {
			return nil, connErr
		}
		return sql.OpenDB(stubConnector{}), nil
	}
	s, err := connectWith(DSN{}, open)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	got := s.Do(func(*sql.DB) error { calls++; return typed("40003", -30081, "recv") })
	if got == nil || calls != 1 || opens != 2 {
		t.Errorf("got=%v calls=%d opens=%d, want an error, 1 call and no further open", got, calls, opens)
	}
}
