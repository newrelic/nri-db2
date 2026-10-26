//go:build !windows

package main

// resolveDiagLogDir returns DIAG_LOG_DIR as configured: /var/log/nri-db2 is the right default here.
func resolveDiagLogDir(dir string) string { return dir }
