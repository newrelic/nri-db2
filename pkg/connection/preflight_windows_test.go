//go:build windows

package connection

import (
	"strings"
	"testing"
)

// Without db2cli64.dll on the search path, Open names the DLL instead of failing in the driver.
func TestOpenReportsMissingDLL(t *testing.T) {
	if cliDLL.Load() == nil {
		t.Skip("db2cli64.dll is installed")
	}
	_, err := Open(DSN{})
	if err == nil || !strings.Contains(err.Error(), "db2cli64.dll") {
		t.Errorf("Open error = %v, want one that names db2cli64.dll", err)
	}
}
