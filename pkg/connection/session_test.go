package connection

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

// stubConnector yields connections that do nothing, so Session tests get real *sql.DB handles.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) { return stubConn{}, nil }
func (stubConnector) Driver() driver.Driver                        { return nil }

type stubConn struct{}

func (stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

// newTestSession returns a session whose opener counts calls and can be told to fail.
func newTestSession(openErr error) (*Session, *int) {
	opens := 0
	s := &Session{
		db: sql.OpenDB(stubConnector{}),
		open: func(DSN) (*sql.DB, error) {
			opens++
			if openErr != nil {
				return nil, openErr
			}
			return sql.OpenDB(stubConnector{}), nil
		},
	}
	return s, &opens
}

func TestDoReconnectsOnceAndReruns(t *testing.T) {
	s, opens := newTestSession(nil)
	calls := 0
	err := s.Do(func(*sql.DB) error {
		calls++
		if calls == 1 {
			return typed("08506", -30108, "SQL30108N")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() = %v, want nil after the re-run", err)
	}
	if calls != 2 || *opens != 1 {
		t.Errorf("calls=%d opens=%d, want 2 and 1", calls, *opens)
	}
}

func TestDoBudgetIsOneReconnectPerRun(t *testing.T) {
	s, opens := newTestSession(nil)
	connErr := typed("40003", -30081, "SQL30081N recv")
	calls := 0
	failing := func(*sql.DB) error { calls++; return connErr }

	if err := s.Do(failing); err == nil {
		t.Fatal("first Do must return the error of the re-run")
	}
	if calls != 2 || *opens != 1 {
		t.Fatalf("first Do: calls=%d opens=%d, want 2 and 1", calls, *opens)
	}
	// The budget is spent: a second query gets no reconnect and no re-run.
	if err := s.Do(failing); err == nil {
		t.Fatal("second Do must fail")
	}
	if calls != 3 || *opens != 1 {
		t.Errorf("second Do: calls=%d opens=%d, want 3 and 1", calls, *opens)
	}
}

func TestDoDoesNotRetryOtherAuthOrRefused(t *testing.T) {
	for name, err := range map[string]error{
		"other":   typed("42501", -551, "SQL0551N"),
		"auth":    typed("08001", -30082, "SQL30082N"),
		"refused": typed("08004", -20157, "SQL20157N"),
	} {
		t.Run(name, func(t *testing.T) {
			s, opens := newTestSession(nil)
			calls := 0
			got := s.Do(func(*sql.DB) error { calls++; return err })
			if !errors.Is(got, err) || calls != 1 || *opens != 0 {
				t.Errorf("got=%v calls=%d opens=%d, want the error, 1 and 0", got, calls, *opens)
			}
		})
	}
}

func TestDoReconnectFailureIsFinal(t *testing.T) {
	boom := typed("08001", -30081, "SQL30081N")
	s, opens := newTestSession(boom)
	calls := 0
	err := s.Do(func(*sql.DB) error { calls++; return boom })
	if err == nil || calls != 1 || *opens != 1 {
		t.Errorf("err=%v calls=%d opens=%d, want error, 1 and 1", err, calls, *opens)
	}
	// After a failed reconnect there is no connection; Do reports a connection-class error.
	if got := s.Do(func(*sql.DB) error { return nil }); Classify(got) != ClassConnection {
		t.Errorf("Do without connection = %v, want a connection-class error", got)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}
