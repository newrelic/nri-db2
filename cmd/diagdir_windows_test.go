//go:build windows

package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

// linuxDiagDefault must equal the DIAG_LOG_DIR default tag, or resolveDiagLogDir stops remapping it
// and Windows would create a Linux-style path on the system drive.
func TestLinuxDiagDefaultMatchesTag(t *testing.T) {
	f, ok := reflect.TypeOf(argumentList{}).FieldByName("DiagLogDir")
	if !ok {
		t.Fatal("argumentList has no DiagLogDir field")
	}
	if got := f.Tag.Get("default"); got != linuxDiagDefault {
		t.Errorf("DiagLogDir default tag = %q, linuxDiagDefault = %q", got, linuxDiagDefault)
	}
}

func TestResolveDiagLogDir(t *testing.T) {
	t.Setenv("ProgramData", `D:\PD`)
	if got, want := resolveDiagLogDir(linuxDiagDefault), filepath.Join(`D:\PD`, "New Relic", "nri-db2", "logs"); got != want {
		t.Errorf("default = %q, want %q", got, want)
	}
	if got := resolveDiagLogDir(`E:\diag`); got != `E:\diag` {
		t.Errorf("explicit value changed to %q", got)
	}
	t.Setenv("ProgramData", "")
	if got, want := resolveDiagLogDir(linuxDiagDefault), `C:\ProgramData\New Relic\nri-db2\logs`; got != want {
		t.Errorf("fallback = %q, want %q", got, want)
	}
}
