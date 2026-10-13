// Package connection is the only package that links the cgo IBM driver.
package connection

import (
	// Registers the "go_ibm_db" database/sql driver. This blank import is what makes the binary
	// link libdb2.so.1, so the rpath check in the build validation proves something.
	_ "github.com/ibmdb/go_ibm_db"
)
