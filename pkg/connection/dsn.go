package connection

import (
	"fmt"
	"strings"

	"github.com/newrelic/nri-db2/pkg/config"
)

const redactedPassword = "<redacted>"

// DSN is the uncataloged connection string. Raw returns the real value for the driver; String
// and %v/%s formatting redact the password, so logging a DSN is safe.
type DSN struct {
	parts []dsnPart
}

type dsnPart struct {
	key, value string
	secret     bool
}

// BuildDSN is a pure function of the configuration. DATABASE is passed as configured: only the
// entity name and the reported attributes are upper-cased.
func BuildDSN(cfg config.Config) DSN {
	parts := []dsnPart{
		{key: "HOSTNAME", value: cfg.Hostname},
		{key: "PORT", value: fmt.Sprint(cfg.Port)},
		{key: "DATABASE", value: cfg.Database},
		{key: "UID", value: cfg.Username},
		{key: "PWD", value: cfg.Password, secret: true},
		{key: "PROTOCOL", value: "TCPIP"},
		{key: "ConnectTimeout", value: fmt.Sprint(cfg.ConnectTimeout)},
		{key: "QueryTimeout", value: fmt.Sprint(cfg.QueryTimeout)},
	}
	if cfg.EnableSSL {
		parts = append(parts, dsnPart{key: "Security", value: "SSL"})
		if cfg.SSLServerCertificate != "" {
			parts = append(parts, dsnPart{key: "SSLServerCertificate", value: cfg.SSLServerCertificate})
		}
	}
	return DSN{parts: parts}
}

// Raw returns the connection string with the password. Pass it only to sql.Open.
func (d DSN) Raw() string { return d.render(false) }

// String returns the connection string with the password redacted.
func (d DSN) String() string { return d.render(true) }

// GoString makes %#v redact the password too; fmt ignores String for %#v.
func (d DSN) GoString() string { return d.String() }

func (d DSN) render(redact bool) string {
	var b strings.Builder
	for i, p := range d.parts {
		if i > 0 {
			b.WriteByte(';')
		}
		v := p.value
		if redact && p.secret && v != "" {
			v = redactedPassword
		}
		b.WriteString(p.key)
		b.WriteByte('=')
		b.WriteString(v)
	}
	return b.String()
}
