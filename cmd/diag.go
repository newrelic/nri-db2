package main

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/log"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/config"
	"github.com/newrelic/nri-db2/pkg/connection"
	"github.com/newrelic/nri-db2/pkg/logs"
	"github.com/newrelic/nri-db2/pkg/queries"
)

// sessionSource reads the diag records through the session, which owns the reconnect budget.
type sessionSource struct {
	session *connection.Session
	// query builds the statement for a row limit; 0 means every row of the window.
	query func(limit int) string
}

func (s sessionSource) ServerNow() (time.Time, error) {
	var rows []queries.Row
	err := s.session.Do(func(db *sql.DB) error {
		var err error
		rows, err = queries.Collect(db, queries.ServerTime)
		return err
	})
	if err != nil {
		return time.Time{}, err
	}
	if len(rows) == 1 {
		if t, ok := rows[0].Time("server_time"); ok {
			return t, nil
		}
	}
	return time.Time{}, errors.New("CURRENT TIMESTAMP returned no timestamp")
}

func (s sessionSource) Rows(start time.Time, limit int) ([]queries.Row, error) {
	var rows []queries.Row
	err := s.session.Do(func(db *sql.DB) error {
		var err error
		rows, err = queries.CollectArgs(db, s.query(limit), start)
		return err
	})
	return rows, err
}

// newDiagSource builds the source with the statement for the configured facility, record types and
// impact (allow-listed in config.Validate).
func newDiagSource(session *connection.Session, cfg config.Config) logs.Source {
	return sessionSource{session, func(limit int) string {
		return queries.DiagHist(cfg.DiagFacility, cfg.DiagRecType, cfg.DiagMinImpact, limit)
	}}
}

// diagDirUsable creates DIAG_LOG_DIR if needed and proves it is writable. When it is not, it logs a
// warning and reports false: diag collection is off for this run, and the metrics and canConnect
// still publish. (An empty path is a configuration error that config.Validate rejects earlier.)
func diagDirUsable(cfg config.Config) bool {
	if err := cfg.CheckDiagDir(); err != nil {
		log.Warn("diag collection is off for this run: %s", err)
		return false
	}
	return true
}

// collectDiag appends the new diag records to the JSONL file. Any failure here (SQL0551N without
// the grant, a full disk, a directory that became unwritable after startup, a lost connection) is a
// warning and the step is skipped: it never changes canConnect, and the stored watermark stays put
// so the next run reads the same window again. A DIAG_LOG_DIR that is not creatable or writable at
// startup never gets here either: diagDirUsable turns diag off for the run.
func collectDiag(src logs.Source, store persist.Storer, cfg config.Config) {
	if store == nil {
		log.Warn("diag collection skipped: no state file")
		return
	}
	if cfg.DiagMinImpact == "NONE" {
		log.Warn("DIAG_MIN_IMPACT=NONE drops every record that has no impact; use an empty value for all records")
	}
	id := logs.Identity{
		EntityName: cfg.EntityName(), ReportingEndpoint: cfg.Endpoint(), Hostname: cfg.Hostname,
		Port: strconv.Itoa(cfg.Port), Database: strings.ToUpper(cfg.Database),
	}
	c := &logs.Collector{
		Store:  store,
		Source: src,
		Writer: &logs.Writer{
			Dir: cfg.DiagLogDir, Name: logs.FileName(id), MaxBytes: int64(cfg.DiagMaxFileMB) << 20,
		},
		ID:         id,
		Overlap:    time.Duration(cfg.DiagOverlapSeconds) * time.Second,
		MaxRecords: cfg.DiagMaxRecordsPerRun,
	}
	res, err := c.Run()
	if err != nil {
		var fe *logs.FileError
		if errors.As(err, &fe) {
			log.Warn("diag records are skipped this run: %s", fe)
			return
		}
		log.Warn("%s failed, diag records are skipped this run: %s", queries.DiagName, connection.Reason(err))
		return
	}
	log.Info("diag logs: %s", res.Describe())
}
