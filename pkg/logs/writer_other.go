//go:build !windows

package logs

import "os"

// rotate moves the full file to <path>.1, replacing any earlier .1.
func rotate(path string) error { return os.Rename(path, path+".1") }
