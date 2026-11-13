package main

import (
	"flag"
	"io"
	"strings"
	"testing"
	"time"
)

// newTestFlags mirrors the kinds of flags the SDK defines from argumentList.
func newTestFlags() (*flag.FlagSet, *bool, *int, *time.Duration, *string) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	b := fs.Bool("enable_ssl", true, "")
	n := fs.Int("port", 50443, "")
	d := fs.Duration("cache_ttl", 6*time.Minute, "")
	s := fs.String("password", "", "")
	fs.Int("diag_overlap_seconds", 10, "")
	fs.Int("query_timeout", 30, "")
	fs.Int("connect_timeout", 10, "")
	return fs, b, n, d, s
}

func TestCheckEnvArgsAccepts(t *testing.T) {
	for _, v := range []string{"true", "false", "1", "0", "TRUE", "True", "t", "F"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("ENABLE_SSL", v)
			fs, _, _, _, _ := newTestFlags()
			if err := checkEnvArgs(fs); err != nil {
				t.Errorf("ENABLE_SSL=%s: %v", v, err)
			}
		})
	}

	t.Setenv("PORT", "50443")
	t.Setenv("CACHE_TTL", "10m")
	t.Setenv("PASSWORD", "any;thing")
	t.Setenv("QUERY_TIMEOUT", "") // empty is ignored, as the SDK ignores it
	fs, _, _, _, _ := newTestFlags()
	if err := checkEnvArgs(fs); err != nil {
		t.Errorf("valid values rejected: %v", err)
	}
}

func TestCheckEnvArgsRejects(t *testing.T) {
	tests := []struct{ env, val, want string }{
		{"ENABLE_SSL", "yes", "ENABLE_SSL"},
		{"ENABLE_SSL", "on", "ENABLE_SSL"},
		{"PORT", "abc", "PORT"},
		{"CACHE_TTL", "10", "CACHE_TTL"},
		{"DIAG_OVERLAP_SECONDS", "abc", "DIAG_OVERLAP_SECONDS"},
		{"QUERY_TIMEOUT", "99999999999999999999", "QUERY_TIMEOUT"},
		{"CONNECT_TIMEOUT", "10s", "CONNECT_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.env+"="+tt.val, func(t *testing.T) {
			t.Setenv(tt.env, tt.val)
			fs, _, _, _, _ := newTestFlags()
			err := checkEnvArgs(fs)
			if err == nil {
				t.Fatalf("%s=%s accepted", tt.env, tt.val)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not name %s", err, tt.want)
			}
		})
	}
}

// Every problem is reported at once, and the check leaves the parsed values alone.
func TestCheckEnvArgsJoinsAndKeepsValues(t *testing.T) {
	t.Setenv("ENABLE_SSL", "yes")
	t.Setenv("PORT", "abc")
	fs, b, n, _, _ := newTestFlags()
	if err := fs.Parse([]string{"-port", "50443"}); err != nil {
		t.Fatal(err)
	}
	err := checkEnvArgs(fs)
	if err == nil || !strings.Contains(err.Error(), "ENABLE_SSL") || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("want both variables in the error, got %v", err)
	}
	if !*b || *n != 50443 {
		t.Errorf("flag values changed: ssl=%t port=%d", *b, *n)
	}
}

func TestCheckEnvArgsNamesTheValue(t *testing.T) {
	t.Setenv("PORT", "abc")
	fs, _, _, _, _ := newTestFlags()
	if err := checkEnvArgs(fs); err == nil || !strings.Contains(err.Error(), `"abc"`) {
		t.Errorf("want the offending value in the message, got %v", err)
	}
}

// A secret is never printed. A string flag cannot fail to parse, so a failing string-like flag
// named PASSWORD is built from a custom Value to prove the redaction.
type rejectAll struct{}

func (*rejectAll) String() string   { return "" }
func (*rejectAll) Set(string) error { return flag.ErrHelp }

func TestCheckEnvArgsNeverPrintsSecrets(t *testing.T) {
	const secret = "s3cr3t-value"
	for _, name := range []string{"password", "username"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(strings.ToUpper(name), secret)
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.Var(&rejectAll{}, name, "")
			err := checkEnvArgs(fs)
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error leaks the value: %v", err)
			}
			if !strings.Contains(err.Error(), strings.ToUpper(name)) {
				t.Errorf("error does not name the variable: %v", err)
			}
		})
	}
}
