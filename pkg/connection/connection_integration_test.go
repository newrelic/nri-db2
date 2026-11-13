//go:build integration

package connection

import (
	"database/sql"
	"os"
	"strconv"
	"testing"

	"github.com/newrelic/nri-db2/pkg/config"
)

// Needs a running Db2 with a monitoring user that has the grants in sql/grants.sql:
// DB2_TEST_HOSTNAME, DB2_TEST_PORT and MONUSER_PASSWORD. Run with `make integration-test`.
func integrationConfig(t *testing.T) config.Config {
	t.Helper()
	host := os.Getenv("DB2_TEST_HOSTNAME")
	password := os.Getenv("MONUSER_PASSWORD")
	if host == "" || password == "" {
		t.Skip("DB2_TEST_HOSTNAME and MONUSER_PASSWORD are required")
	}
	port := 50000 // the plaintext port, because EnableSSL is false below
	if p := os.Getenv("DB2_TEST_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("DB2_TEST_PORT: %v", err)
		}
		port = n
	}
	return config.Config{
		Hostname:       host,
		Port:           port,
		EnableSSL:      false,
		Database:       "testdb",
		Username:       "monuser",
		Password:       password,
		ConnectTimeout: config.DefaultConnectTimeout,
		QueryTimeout:   config.DefaultQueryTimeout,
	}
}

func TestConnectIntegration(t *testing.T) {
	s, err := Connect(BuildDSN(integrationConfig(t)))
	if err != nil {
		t.Fatalf("Connect: %s", Reason(err))
	}
	defer s.Close()
	err = s.Do(func(db *sql.DB) error { return db.Ping() })
	if err != nil {
		t.Fatalf("Ping: %s", Reason(err))
	}
}

func TestWrongPasswordIsAuthIntegration(t *testing.T) {
	cfg := integrationConfig(t)
	cfg.Password = "WrongPass1"
	db, err := Open(BuildDSN(cfg))
	if err == nil {
		db.Close()
		t.Fatal("expected an authentication error")
	}
	if got := Classify(err); got != ClassAuth {
		t.Errorf("Classify() = %v, want %v (%s)", got, ClassAuth, Reason(err))
	}
}
