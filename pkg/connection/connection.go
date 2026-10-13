package connection

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
)

const (
	driverName = "go_ibm_db"

	// connRetriesEnv is the driver knob that stops the client from retrying a failed communication-level connect.
	connRetriesEnv = "DB2_MAX_CLIENT_CONNRETRIES"
)

// ensureConnRetriesEnv sets DB2_MAX_CLIENT_CONNRETRIES=0 unless the process already has the
// variable, so a value the user put in the integration env block is respected. It must run
// before the first sql.Open.
func ensureConnRetriesEnv(lookup func(string) (string, bool), set func(key, value string) error) error {
	if _, ok := lookup(connRetriesEnv); ok {
		return nil
	}
	return set(connRetriesEnv, "0")
}

// Open connects with the DSN, caps the pool at one connection and pings. Go context deadlines
// are not used: they only fire after the blocking cgo call returns, so the DSN timeouts
// (ConnectTimeout, QueryTimeout) bound the calls instead.
func Open(dsn DSN) (*sql.DB, error) {
	if err := ensureConnRetriesEnv(os.LookupEnv, os.Setenv); err != nil {
		return nil, fmt.Errorf("set %s: %w", connRetriesEnv, err)
	}
	db, err := sql.Open(driverName, dsn.Raw())
	if err != nil {
		return nil, err
	}
	// The driver never reports a dead connection as driver.ErrBadConn in practice.
	// One connection per run keeps the reconnect in Session.Do explicit.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Session is one run's connection plus its single reconnect budget. The budget is shared by the
// connect step and the queries: once it is spent, a further connection-class error is final.
type Session struct {
	dsn         DSN
	db          *sql.DB
	reconnected bool
	open        func(DSN) (*sql.DB, error)
}

// Connect opens the session and, on a retryable connection error, retries exactly once,
// which spends the budget. Authentication failures and server refusals (08004) are not retried.
func Connect(dsn DSN) (*Session, error) { return connectWith(dsn, Open) }

// connectWith is Connect with an injectable opener, so the budget rules are testable.
func connectWith(dsn DSN, open func(DSN) (*sql.DB, error)) (*Session, error) {
	s := &Session{dsn: dsn, open: open}
	db, err := s.open(dsn)
	if err != nil && Classify(err).Retryable() {
		s.reconnected = true
		db, err = s.open(dsn)
	}
	if err != nil {
		return nil, err
	}
	s.db = db
	return s, nil
}

// Do runs fn against the connection. On a retryable connection-class error and with the budget
// unspent, it reconnects once and runs fn again; a second failure is returned as is. fn must be
// safe to run twice (a read-only query).
func (s *Session) Do(fn func(*sql.DB) error) error {
	if s.db == nil {
		return fmt.Errorf("no connection: %w", driver.ErrBadConn)
	}
	err := fn(s.db)
	if err == nil || !Classify(err).Retryable() || s.reconnected {
		return err
	}
	s.reconnected = true
	_ = s.db.Close()
	s.db = nil
	db, rerr := s.open(s.dsn)
	if rerr != nil {
		return rerr
	}
	s.db = db
	return fn(s.db)
}

// Close releases the connection.
func (s *Session) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}
