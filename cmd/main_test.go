package main

import (
	"reflect"
	"testing"

	"github.com/newrelic/nri-db2/pkg/config"
)

// The declared defaults are the documented configuration defaults.
func TestArgumentDefaults(t *testing.T) {
	want := map[string]string{
		"Port": "50000", "EnableSSL": "false", "ConnectTimeout": "10", "QueryTimeout": "30", "RunTimeout": "100",
		"CollectTopQueries": "false", "TopQueriesLimit": "20",
		"CollectLongRunningQueries": "false", "LongRunningThresholdSeconds": "60",
		"CollectLockWaits": "false", "CollectDiagLogs": "false",
		"DiagLogDir": "/var/log/nri-db2", "DiagFacility": "MAIN", "DiagRectype": "ALL",
		"DiagMinImpact": "", "DiagOverlapSeconds": "10", "DiagMaxRecordsPerRun": "5000",
		"DiagMaxFileMb": "50", "Hostname": "", "Database": "", "Username": "", "Password": "",
		"SSLServerCertificate": "", "CustomMetricsQuery": "", "CustomMetricsConfig": "",
	}
	typ := reflect.TypeOf(argumentList{})
	for name, def := range want {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Errorf("argumentList has no field %s", name)
			continue
		}
		if got := f.Tag.Get("default"); got != def {
			t.Errorf("%s default = %q, want %q", name, got, def)
		}
	}
}

// Every argument reaches its Config field, and the diag filters are trimmed and upper-cased.
func TestArgumentListConfig(t *testing.T) {
	a := argumentList{
		Hostname: "db2.example.com", Port: 50443, Database: "SAMPLE",
		Username: "monuser", Password: "secret",
		EnableSSL: true, SSLServerCertificate: "/etc/db2/server.crt",
		ConnectTimeout: 5, QueryTimeout: 15, RunTimeout: 45,
		CustomMetricsQuery: "SELECT 1 FROM SYSIBM.SYSDUMMY1", CustomMetricsConfig: "/etc/q.yml",
		CollectTopQueries: true, TopQueriesLimit: 7,
		CollectLongRunningQueries: true, LongRunningThresholdSeconds: 90,
		CollectLockWaits: true,
		CollectDiagLogs:  true, DiagLogDir: "/var/log/x",
		DiagFacility: " main ", DiagRectype: " error ", DiagMinImpact: " high ",
		DiagOverlapSeconds: 3, DiagMaxRecordsPerRun: 100, DiagMaxFileMb: 8,
	}
	want := config.Config{
		Hostname: "db2.example.com", Port: 50443, Database: "SAMPLE",
		Username: "monuser", Password: "secret",
		EnableSSL: true, SSLServerCertificate: "/etc/db2/server.crt",
		ConnectTimeout: 5, QueryTimeout: 15, RunTimeout: 45,
		CustomMetricsQuery: "SELECT 1 FROM SYSIBM.SYSDUMMY1", CustomMetricsConfig: "/etc/q.yml",
		CollectTopQueries: true, TopQueriesLimit: 7,
		CollectLongRunningQueries: true, LongRunningThresholdSeconds: 90,
		CollectLockWaits: true,
		CollectDiagLogs:  true, DiagLogDir: "/var/log/x",
		DiagFacility: "MAIN", DiagRecType: "ERROR", DiagMinImpact: "HIGH",
		DiagOverlapSeconds: 3, DiagMaxRecordsPerRun: 100, DiagMaxFileMB: 8,
	}
	got := a.config()
	if got != want {
		t.Errorf("config() =\n%+v\nwant\n%+v", got, want)
	}

	// A new argument must be set above and reach a Config field: every field of both must be
	// non-zero, and the two structs must have the same number of fields.
	args := reflect.ValueOf(a)
	own := 0
	for i := 0; i < args.NumField(); i++ {
		f := args.Type().Field(i)
		if f.Anonymous { // sdkArgs.DefaultArgumentList
			continue
		}
		own++
		if args.Field(i).IsZero() {
			t.Errorf("argumentList.%s is zero in the test input", f.Name)
		}
	}
	cfg := reflect.ValueOf(got)
	for i := 0; i < cfg.NumField(); i++ {
		if cfg.Field(i).IsZero() {
			t.Errorf("config.Config.%s is zero: it is not set from an argument", cfg.Type().Field(i).Name)
		}
	}
	if own != cfg.NumField() {
		t.Errorf("argumentList has %d own fields but Config has %d", own, cfg.NumField())
	}
}
