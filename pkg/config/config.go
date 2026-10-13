// Package config holds the typed integration configuration, its defaults and
// validation. It has no Db2 or SDK dependency, so it is unit-testable without cgo.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Config defaults
const (
	DefaultPort           = 50000
	DefaultConnectTimeout = 10
	DefaultQueryTimeout   = 30
	DefaultRunTimeout     = 100

	redacted = "<redacted>"
	// dsnSeparator terminates a value in the DSN and cannot be escaped.
	dsnSeparator = ";"
)

// Allow-lists for the PD_GET_DIAG_HIST arguments. The impact list starts with the empty string,
// the default, which returns every record. NONE is accepted because typing it is deliberate, but
// it drops every record that has no impact (151 of 285 rows on 12.1).
var (
	diagFacilities = []string{"MAIN", "OPTSTATS", "ALL"}
	diagImpacts    = []string{"", "UNLIKELY", "POTENTIAL", "IMMEDIATE", "CRITICAL", "NONE"}
	diagRecTypes   = []string{"E", "D", "EI", "EX", "DI", "DX"}
)

// checkRecType accepts ALL or record types joined with '+', for example E+D.
func checkRecType(v string) error {
	v = strings.ToUpper(v)
	if v == "ALL" {
		return nil
	}
	for _, tok := range strings.Split(v, "+") {
		if !slices.Contains(diagRecTypes, tok) {
			return fmt.Errorf("must be ALL or record types joined with '+' from %s, got %q", strings.Join(diagRecTypes, ", "), v)
		}
	}
	return nil
}

// Config is the validated view of the integration arguments.
type Config struct {
	Hostname string
	Port     int
	Database string
	Username string
	Password string

	EnableSSL            bool
	SSLServerCertificate string
	ConnectTimeout       int
	QueryTimeout         int
	// RunTimeout is the whole-run deadline in seconds; 0 disables the watchdog.
	RunTimeout int

	CustomMetricsQuery  string
	CustomMetricsConfig string

	CollectTopQueries           bool
	TopQueriesLimit             int
	CollectLongRunningQueries   bool
	LongRunningThresholdSeconds int
	CollectLockWaits            bool

	CollectDiagLogs      bool
	DiagLogDir           string
	DiagFacility         string
	DiagRecType          string
	DiagMinImpact        string
	DiagOverlapSeconds   int
	DiagMaxRecordsPerRun int
	DiagMaxFileMB        int
}

// Endpoint returns HOSTNAME:PORT, the entity endpoint.
func (c Config) Endpoint() string {
	return fmt.Sprintf("%s:%d", c.Hostname, c.Port)
}

// EntityName returns HOSTNAME/DATABASE with the database upper-cased. The port is not part of the
// name, so two instances on one host that serve the same database name get the same entity.
func (c Config) EntityName() string {
	return c.Hostname + "/" + strings.ToUpper(c.Database)
}

// StateKey identifies the instance in file names. Unlike EntityName it includes the port, so two
// instances on one host never share a state file.
func (c Config) StateKey() string {
	return c.Endpoint() + "/" + strings.ToUpper(c.Database)
}

// ExtrasEnabled reports whether any of the New Relic extras collectors is on.
func (c Config) ExtrasEnabled() bool {
	return c.CollectTopQueries || c.CollectLongRunningQueries || c.CollectLockWaits
}

// Validate applies the rules of the design ("Validation rules") and joins every
// violation into one error. It never includes the password in the message.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	required := []struct{ key, val string }{
		{"HOSTNAME", c.Hostname},
		{"DATABASE", c.Database},
		{"USERNAME", c.Username},
		{"PASSWORD", c.Password},
	}
	for _, r := range required {
		if r.val == "" {
			add("%s is required", r.key)
		}
	}

	if c.Port < 1 || c.Port > 65535 {
		add("PORT must be an integer in 1-65535, got %d", c.Port)
	}

	// A ';' terminates a DSN value and brace quoting does not work, so it is rejected up front.
	unsafe := []struct{ key, val string }{
		{"HOSTNAME", c.Hostname},
		{"DATABASE", c.Database},
		{"USERNAME", c.Username},
		{"PASSWORD", c.Password},
		{"SSL_SERVER_CERTIFICATE", c.SSLServerCertificate},
	}
	for _, u := range unsafe {
		if strings.Contains(u.val, dsnSeparator) {
			add("%s must not contain ';'", u.key)
		}
	}

	if c.ConnectTimeout < 1 {
		add("CONNECT_TIMEOUT must be >= 1, got %d", c.ConnectTimeout)
	}
	if c.QueryTimeout < 1 {
		add("QUERY_TIMEOUT must be >= 1, got %d", c.QueryTimeout)
	}

	if c.RunTimeout < 0 {
		add("RUN_TIMEOUT must be 0 (disabled) or >= 1, got %d", c.RunTimeout)
	}

	// Fail closed: a configured certificate must never leave the connection in plaintext.
	if c.SSLServerCertificate != "" && !c.EnableSSL {
		add("SSL_SERVER_CERTIFICATE is set but ENABLE_SSL is not true")
	}

	if c.TopQueriesLimit < 1 {
		add("TOP_QUERIES_LIMIT must be >= 1, got %d", c.TopQueriesLimit)
	}
	if c.LongRunningThresholdSeconds < 1 {
		add("LONG_RUNNING_THRESHOLD_SECONDS must be >= 1, got %d", c.LongRunningThresholdSeconds)
	}
	if c.DiagOverlapSeconds < 0 {
		add("DIAG_OVERLAP_SECONDS must be >= 0, got %d", c.DiagOverlapSeconds)
	}
	if c.DiagMaxRecordsPerRun < 1 {
		add("DIAG_MAX_RECORDS_PER_RUN must be >= 1, got %d", c.DiagMaxRecordsPerRun)
	}
	if c.DiagMaxFileMB < 1 {
		add("DIAG_MAX_FILE_MB must be >= 1, got %d", c.DiagMaxFileMB)
	}
	if c.CollectDiagLogs {
		if c.DiagLogDir == "" {
			add("DIAG_LOG_DIR %q is not usable: empty path", c.DiagLogDir)
		}
		// These three are placed in the statement as literals, so they are checked against
		// allow-lists (design: "Log collection").
		if !slices.Contains(diagFacilities, strings.ToUpper(c.DiagFacility)) {
			add("DIAG_FACILITY must be one of %s, got %q", strings.Join(diagFacilities, ", "), c.DiagFacility)
		}
		if err := checkRecType(c.DiagRecType); err != nil {
			add("DIAG_RECTYPE %v", err)
		}
		if !slices.Contains(diagImpacts, strings.ToUpper(c.DiagMinImpact)) {
			add("DIAG_MIN_IMPACT must be empty or one of %s, got %q", strings.Join(diagImpacts[1:], ", "), c.DiagMinImpact)
		}
	}

	return errors.Join(errs...)
}

// String renders the configuration for logs with the password redacted.
func (c Config) String() string {
	pw := ""
	if c.Password != "" {
		pw = redacted
	}
	return fmt.Sprintf(
		"hostname=%q port=%d database=%q username=%q password=%q ssl=%t ssl_server_certificate=%q connect_timeout=%d query_timeout=%d",
		c.Hostname, c.Port, c.Database, c.Username, pw,
		c.EnableSSL, c.SSLServerCertificate, c.ConnectTimeout, c.QueryTimeout,
	)
}

// GoString makes %#v redact the password too; fmt ignores String for %#v.
func (c Config) GoString() string { return c.String() }

// diagDirMode is the mode of a DIAG_LOG_DIR that the integration creates. Diag records can hold
// user IDs and client addresses, so the directory is not world-readable.
const diagDirMode = 0o750

// CheckDiagDir creates DIAG_LOG_DIR if needed and proves it is writable. It touches the file system,
// so callers run it only when diag records will be written, not on inventory-only or events-only
// runs. Validate has already rejected an empty path.
func (c Config) CheckDiagDir() error {
	if err := checkWritableDir(c.DiagLogDir); err != nil {
		return fmt.Errorf("DIAG_LOG_DIR %q is not usable: %w", c.DiagLogDir, err)
	}
	return nil
}

// checkWritableDir creates dir if needed and proves it is writable.
func checkWritableDir(dir string) error {
	if dir == "" {
		return errors.New("empty path")
	}
	if err := os.MkdirAll(dir, diagDirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".nri-db2-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(filepath.Clean(name))
}
