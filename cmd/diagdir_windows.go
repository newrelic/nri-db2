//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// linuxDiagDefault is the DIAG_LOG_DIR default declared on argumentList. It is a path that makes
// no sense on Windows, so only that exact value is replaced; anything the user set is kept.
const linuxDiagDefault = "/var/log/nri-db2"

// resolveDiagLogDir maps the declared default to %ProgramData%\New Relic\nri-db2\logs.
func resolveDiagLogDir(dir string) string {
	if dir != linuxDiagDefault {
		return dir
	}
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "New Relic", "nri-db2", "logs")
}
