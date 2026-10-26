//go:build windows

package connection

import (
	"fmt"
	"os"
	"syscall"

	"github.com/newrelic/infra-integrations-sdk/v3/log"
)

// cliDLL is the IBM CLI driver library that go_ibm_db loads lazily on Windows. It uses the
// same name, so the same DLL search order finds it.
var cliDLL = syscall.NewLazyDLL("db2cli64.dll")

// preflight proves the IBM CLI driver DLL can be loaded, so a missing clidriver gives an
// actionable error instead of the driver's "unknown driver" failure. Windows reports "module not
// found" both when db2cli64.dll is missing and when one of the DLLs it imports is, so the hint
// names both causes.
func preflight() error {
	if err := cliDLL.Load(); err != nil {
		return fmt.Errorf(`load db2cli64.dll: %w (db2cli64.dll or a DLL it needs was not found: add the clidriver bin directory to the system PATH, make sure the Microsoft Visual C++ 2015-2022 x64 runtime is installed, and restart the newrelic-infra service)`, err)
	}
	// The agent starts integrations without USERPROFILE, and the IBM client then fails every
	// connect with SQL5005N and a blank SQLSTATE. Say so before that opaque error.
	if os.Getenv("USERPROFILE") == "" {
		log.Warn("USERPROFILE is not set: the IBM client fails every connect with SQL5005N without it; set USERPROFILE in the env block of the integration config (see the Windows section of the README)")
	}
	return nil
}
