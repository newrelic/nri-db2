//go:build !windows

package connection

// preflight has nothing to check outside Windows: the Linux binary links libdb2 at build time.
func preflight() error { return nil }
