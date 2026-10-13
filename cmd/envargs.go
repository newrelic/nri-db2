package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"
)

// secretEnv names the arguments whose value is never echoed in an error.
var secretEnv = map[string]bool{"PASSWORD": true, "USERNAME": true}

// checkEnvArgs rejects environment variables that the SDK could not parse. SDK v3 applies them in
// args.SetupArgs and ignores the error of Value.Set, so ENABLE_SSL=yes silently becomes false and
// DIAG_OVERLAP_SECONDS=abc becomes 0. Every flag is re-parsed with a fresh value of its own type,
// the same way the SDK parses it, so the check never changes the real values (a command-line flag
// still wins over the environment). All problems are joined into one error.
func checkEnvArgs(fs *flag.FlagSet) error {
	var errs []error
	fs.VisitAll(func(f *flag.Flag) {
		name := strings.ToUpper(f.Name)
		raw := os.Getenv(name)
		if raw == "" { // the SDK ignores an empty variable
			return
		}
		typ := reflect.TypeOf(f.Value)
		if typ == nil || typ.Kind() != reflect.Pointer {
			return
		}
		fresh, ok := reflect.New(typ.Elem()).Interface().(flag.Value)
		if !ok {
			return
		}
		if err := fresh.Set(raw); err != nil {
			shown := fmt.Sprintf("%q ", raw)
			if secretEnv[name] {
				shown = ""
			}
			errs = append(errs, fmt.Errorf("environment variable %s %sis not a valid %s", name, shown, kindOf(f)))
		}
	})
	return errors.Join(errs...)
}

// kindOf names the expected format of a flag in an error message.
func kindOf(f *flag.Flag) string {
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return "value"
	}
	switch g.Get().(type) {
	case bool:
		return "boolean (true, false, 1 or 0)"
	case int, int64, uint, uint64:
		return "integer"
	case time.Duration:
		return "duration (for example 30s)"
	default:
		return "value"
	}
}
