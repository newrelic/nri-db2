package connection

import (
	"fmt"
	"strings"
	"testing"

	"github.com/newrelic/nri-db2/pkg/config"
)

func baseConfig() config.Config {
	return config.Config{
		Hostname:       "db2-121",
		Port:           50000,
		Database:       "testdb",
		Username:       "monuser",
		Password:       "MonPass1",
		ConnectTimeout: 10,
		QueryTimeout:   30,
	}
}

func TestBuildDSN(t *testing.T) {
	withSSL := baseConfig()
	withSSL.EnableSSL = true
	withSSL.SSLServerCertificate = "/certs/db2-121.arm"
	sslNoCert := baseConfig()
	sslNoCert.EnableSSL = true

	tests := []struct {
		name string
		cfg  config.Config
		raw  string
	}{
		{
			"plain",
			baseConfig(),
			"HOSTNAME=db2-121;PORT=50000;DATABASE=testdb;UID=monuser;PWD=MonPass1;PROTOCOL=TCPIP;ConnectTimeout=10;QueryTimeout=30",
		},
		{
			"ssl with certificate",
			withSSL,
			"HOSTNAME=db2-121;PORT=50000;DATABASE=testdb;UID=monuser;PWD=MonPass1;PROTOCOL=TCPIP;ConnectTimeout=10;QueryTimeout=30;Security=SSL;SSLServerCertificate=/certs/db2-121.arm",
		},
		{
			"ssl without certificate",
			sslNoCert,
			"HOSTNAME=db2-121;PORT=50000;DATABASE=testdb;UID=monuser;PWD=MonPass1;PROTOCOL=TCPIP;ConnectTimeout=10;QueryTimeout=30;Security=SSL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := BuildDSN(tt.cfg)
			if got := d.Raw(); got != tt.raw {
				t.Errorf("Raw() =\n%s\nwant\n%s", got, tt.raw)
			}
			wantRedacted := strings.Replace(tt.raw, "PWD=MonPass1", "PWD=<redacted>", 1)
			if got := d.String(); got != wantRedacted {
				t.Errorf("String() =\n%s\nwant\n%s", got, wantRedacted)
			}
		})
	}
}

func TestDSNNeverFormatsPassword(t *testing.T) {
	d := BuildDSN(baseConfig())
	for _, s := range []string{d.String(), fmt.Sprintf("%v", d), fmt.Sprintf("%+v", d), fmt.Sprint(d)} {
		if strings.Contains(s, "MonPass1") {
			t.Errorf("formatted DSN contains the password: %s", s)
		}
	}
}

func TestDatabaseNotUpperCased(t *testing.T) {
	if got := BuildDSN(baseConfig()).Raw(); !strings.Contains(got, "DATABASE=testdb;") {
		t.Errorf("DATABASE must be passed as configured: %s", got)
	}
}

func TestDSNGoStringRedacts(t *testing.T) {
	d := BuildDSN(baseConfig())
	if got := fmt.Sprintf("%#v", d); strings.Contains(got, "MonPass1") {
		t.Errorf("%%#v of a DSN contains the password: %s", got)
	}
}
