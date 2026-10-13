package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Hostname:                    "db2-121",
		Port:                        50000,
		Database:                    "testdb",
		Username:                    "monuser",
		Password:                    "MonPass1",
		ConnectTimeout:              DefaultConnectTimeout,
		QueryTimeout:                DefaultQueryTimeout,
		RunTimeout:                  DefaultRunTimeout,
		TopQueriesLimit:             20,
		LongRunningThresholdSeconds: 60,
		DiagFacility:                "MAIN",
		DiagRecType:                 "ALL",
		DiagOverlapSeconds:          10,
		DiagMaxRecordsPerRun:        5000,
		DiagMaxFileMB:               50,
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // substring; empty means valid
	}{
		{"valid", func(*Config) {}, ""},
		{"empty hostname", func(c *Config) { c.Hostname = "" }, "HOSTNAME is required"},
		{"empty database", func(c *Config) { c.Database = "" }, "DATABASE is required"},
		{"empty username", func(c *Config) { c.Username = "" }, "USERNAME is required"},
		{"empty password", func(c *Config) { c.Password = "" }, "PASSWORD is required"},
		{"port zero", func(c *Config) { c.Port = 0 }, "PORT must be"},
		{"port too high", func(c *Config) { c.Port = 70000 }, "PORT must be"},
		{"port max ok", func(c *Config) { c.Port = 65535 }, ""},
		{"semicolon in password", func(c *Config) { c.Password = "Mon;Pass1" }, "PASSWORD must not contain ';'"},
		{"semicolon in hostname", func(c *Config) { c.Hostname = "db2;x" }, "HOSTNAME must not contain ';'"},
		{"semicolon in database", func(c *Config) { c.Database = "db;x" }, "DATABASE must not contain ';'"},
		{"semicolon in username", func(c *Config) { c.Username = "mon;user" }, "USERNAME must not contain ';'"},
		{"braces in password ok", func(c *Config) { c.Password = "{Mon}Pass1" }, ""},
		{"connect timeout zero", func(c *Config) { c.ConnectTimeout = 0 }, "CONNECT_TIMEOUT must be >= 1"},
		{"run timeout zero disables", func(c *Config) { c.RunTimeout = 0 }, ""},
		{"run timeout one ok", func(c *Config) { c.RunTimeout = 1 }, ""},
		{"run timeout negative", func(c *Config) { c.RunTimeout = -1 }, "RUN_TIMEOUT must be 0 (disabled) or >= 1"},
		{"query timeout zero", func(c *Config) { c.QueryTimeout = 0 }, "QUERY_TIMEOUT must be >= 1"},
		{"cert without ssl", func(c *Config) { c.SSLServerCertificate = "/certs/x.arm" }, "SSL_SERVER_CERTIFICATE is set but ENABLE_SSL"},
		{"ssl without cert ok", func(c *Config) { c.EnableSSL = true }, ""},
		{"ssl with cert ok", func(c *Config) { c.EnableSSL = true; c.SSLServerCertificate = "/certs/x.arm" }, ""},
		{"top queries limit zero", func(c *Config) { c.TopQueriesLimit = 0 }, "TOP_QUERIES_LIMIT must be >= 1"},
		{"long running threshold zero", func(c *Config) { c.LongRunningThresholdSeconds = 0 }, "LONG_RUNNING_THRESHOLD_SECONDS must be >= 1"},
		{"overlap negative", func(c *Config) { c.DiagOverlapSeconds = -1 }, "DIAG_OVERLAP_SECONDS must be >= 0"},
		{"overlap zero ok", func(c *Config) { c.DiagOverlapSeconds = 0 }, ""},
		{"max records zero", func(c *Config) { c.DiagMaxRecordsPerRun = 0 }, "DIAG_MAX_RECORDS_PER_RUN must be >= 1"},
		{"max file zero", func(c *Config) { c.DiagMaxFileMB = 0 }, "DIAG_MAX_FILE_MB must be >= 1"},
		{"diag dir writable", func(c *Config) {
			c.CollectDiagLogs = true
			c.DiagLogDir = filepath.Join(t.TempDir(), "sub")
		}, ""},
		{"diag dir empty", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = "" }, "DIAG_LOG_DIR"},
		{"diag facility bad", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagFacility = "X'; DROP" }, "DIAG_FACILITY"},
		{"diag facility all ok", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagFacility = "ALL" }, ""},
		{"diag rectype joined ok", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagRecType = "E+D+DX" }, ""},
		{"diag rectype bad token", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagRecType = "E+Z" }, "DIAG_RECTYPE"},
		{"diag rectype empty", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagRecType = "" }, "DIAG_RECTYPE"},
		{"diag rectype quote", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagRecType = "E'" }, "DIAG_RECTYPE"},
		{"diag impact empty ok", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagMinImpact = "" }, ""},
		{"diag impact critical ok", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagMinImpact = "CRITICAL" }, ""},
		{"diag impact none typed ok", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagMinImpact = "NONE" }, ""},
		{"diag impact bad", func(c *Config) { c.CollectDiagLogs = true; c.DiagLogDir = t.TempDir(); c.DiagMinImpact = "HIGH" }, "DIAG_MIN_IMPACT"},
		{"diag lists ignored when disabled", func(c *Config) { c.DiagFacility = "bad"; c.DiagRecType = "bad"; c.DiagMinImpact = "bad" }, ""},
		{"diag dir ignored when disabled", func(c *Config) { c.DiagLogDir = "/dev/null/sub" }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestStringRedactsPassword(t *testing.T) {
	c := validConfig()
	c.Password = "Sup3rSecret"
	for _, s := range []string{c.String(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprint(c)} {
		if strings.Contains(s, "Sup3rSecret") {
			t.Fatalf("output leaks password: %s", s)
		}
	}
	if !strings.Contains(c.String(), "<redacted>") {
		t.Fatalf("want redaction marker, got %s", c.String())
	}
}

func TestEntityName(t *testing.T) {
	c := validConfig()
	if got, want := c.EntityName(), "db2-121/TESTDB"; got != want {
		t.Fatalf("EntityName = %q, want %q", got, want)
	}
	if got, want := c.StateKey(), "db2-121:50000/TESTDB"; got != want {
		t.Fatalf("StateKey = %q, want %q", got, want)
	}
	other := c
	other.Port = 50001
	if c.EntityName() != other.EntityName() || c.StateKey() == other.StateKey() {
		t.Fatalf("the port must change the state key but not the entity name")
	}
	if got, want := c.Endpoint(), "db2-121:50000"; got != want {
		t.Fatalf("Endpoint = %q, want %q", got, want)
	}
}

func TestValidateRejectsSeparatorInCertificate(t *testing.T) {
	c := validConfig()
	c.EnableSSL = true
	c.SSLServerCertificate = "/x.arm;PWD=other"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "SSL_SERVER_CERTIFICATE must not contain ';'") {
		t.Fatalf("want a ';' error for SSL_SERVER_CERTIFICATE, got %v", err)
	}
}

func TestExtrasEnabled(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Config)
		want bool
	}{
		{"all off", func(*Config) {}, false},
		{"top queries", func(c *Config) { c.CollectTopQueries = true }, true},
		{"long running", func(c *Config) { c.CollectLongRunningQueries = true }, true},
		{"lock waits", func(c *Config) { c.CollectLockWaits = true }, true},
		{"diag logs are not an extra", func(c *Config) { c.CollectDiagLogs = true }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mod(&c)
			if got := c.ExtrasEnabled(); got != tt.want {
				t.Errorf("ExtrasEnabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCheckDiagDir(t *testing.T) {
	c := validConfig()
	c.CollectDiagLogs = true
	c.DiagLogDir = filepath.Join(t.TempDir(), "sub")
	if err := c.CheckDiagDir(); err != nil {
		t.Fatalf("CheckDiagDir: %v", err)
	}
	st, err := os.Stat(c.DiagLogDir)
	if err != nil {
		t.Fatal(err)
	}
	// The mode is masked by the umask, which can only remove bits.
	if got := st.Mode().Perm(); got&^0o750 != 0 {
		t.Errorf("directory mode %o has bits outside 0750", got)
	}
	entries, err := os.ReadDir(c.DiagLogDir)
	if err != nil || len(entries) != 0 {
		t.Errorf("write-check file left behind: %v %v", entries, err)
	}

	c.DiagLogDir = "/dev/null/sub"
	err = c.CheckDiagDir()
	if err == nil || !strings.Contains(err.Error(), "DIAG_LOG_DIR") {
		t.Errorf("not creatable: want a DIAG_LOG_DIR error, got %v", err)
	}
}
