// Package connection is the only package that links the IBM driver: through cgo on Linux, and by
// loading db2cli64.dll at run time on Windows (see preflight_windows.go).
package connection

import (
	// Registers the "go_ibm_db" database/sql driver. On Linux this blank import is what makes the
	// binary link libdb2.so.1, so the rpath check in the build validation proves something.
	_ "github.com/ibmdb/go_ibm_db"
)
