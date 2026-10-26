//go:build windows

package logs

import (
	"errors"
	"os"
	"syscall"

	"github.com/newrelic/infra-integrations-sdk/v3/log"
)

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall does not define.
const errSharingViolation syscall.Errno = 32

// rotate moves the full file to <path>.1, replacing any earlier .1. Windows refuses to rename a
// file another process holds open without FILE_SHARE_DELETE, which a log tailer can do. That is
// not an error for the run: the file keeps growing and the next run tries again. Any other error,
// such as access denied, is returned: it will not clear by itself and must not hide as a warning.
func rotate(path string) error {
	err := os.Rename(path, path+".1")
	if errors.Is(err, errSharingViolation) {
		log.Warn("diag file %s is in use and cannot be rotated: appending to it, rotation is retried next run: %s", path, err)
		return nil
	}
	return err
}
