package main

import (
	"crypto/md5" //nolint:gosec // a file name, not security
	"encoding/hex"
	"flag"
	"os"
	"slices"
	"strconv"
	"strings"

	sdkArgs "github.com/newrelic/infra-integrations-sdk/v3/args"
	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"
	"github.com/newrelic/infra-integrations-sdk/v3/log"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/config"
	"github.com/newrelic/nri-db2/pkg/connection"
	"github.com/newrelic/nri-db2/pkg/custom"
	"github.com/newrelic/nri-db2/pkg/health"
	"github.com/newrelic/nri-db2/pkg/metrics"
	"github.com/newrelic/nri-db2/pkg/queries"
)

const (
	integrationName  = "com.newrelic.labs.db2"
	entityNamespace  = "db2-instance"
	databaseSample   = "Db2DatabaseSample"
	bufferpoolSample = "Db2BufferpoolSample"
	tablespaceSample = "Db2TablespaceSample"

	// stateFilePrefix names the state file (tablespace states and the diag watermark). It must not start with the integration
	// name: the SDK deletes expired files that match "<integration name>-*.json".
	stateFilePrefix = "nri-db2-state-"

	exitOK            = 0
	exitInternalError = 1
	exitInvalidConfig = 2
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

// argumentList declares one field per configuration key.
// The SDK derives the flag name from the field name (CollectDiagLogs -> collect_diag_logs)
// and the environment variable from the flag name (COLLECT_DIAG_LOGS).
type argumentList struct {
	sdkArgs.DefaultArgumentList

	Hostname string `default:"" help:"Db2 server host name or address. Must always be set explicitly."`
	Port     int    `default:"50000" help:"Db2 listener port."`
	Database string `default:"" help:"Database name (alias). One block monitors one database."`
	Username string `default:"" help:"Monitoring user."`
	Password string `default:"" help:"Password. Must not contain ';'."`

	EnableSSL            bool   `default:"false" help:"Use TLS to the server."`
	SSLServerCertificate string `default:"" help:"Path to the server certificate the client trusts. Requires ENABLE_SSL."`
	ConnectTimeout       int    `default:"10" help:"Seconds to wait when establishing the connection."`
	QueryTimeout         int    `default:"30" help:"Per-statement cancel timeout in seconds."`
	RunTimeout           int    `default:"100" help:"Seconds after which a run that is still going publishes canConnect=0 and exits. 0 disables."`

	CustomMetricsQuery  string `default:"" help:"One inline custom query."`
	CustomMetricsConfig string `default:"" help:"Path to a YAML file of custom queries."`

	CollectTopQueries           bool `default:"false" help:"Enable top-SQL collection."`
	TopQueriesLimit             int  `default:"20" help:"Maximum statements reported per run."`
	CollectLongRunningQueries   bool `default:"false" help:"Enable long-running SQL collection."`
	LongRunningThresholdSeconds int  `default:"60" help:"Report activities running longer than this many seconds."`
	CollectLockWaits            bool `default:"false" help:"Enable lock-wait collection."`
	StripSqlLiterals            bool `default:"true" help:"Replace literal values and comments in statementText with placeholders."`

	CollectDiagLogs      bool   `default:"false" help:"Collect db2diag.log records through SYSPROC.PD_GET_DIAG_HIST."`
	DiagLogDir           string `default:"/var/log/nri-db2" help:"Directory for the diag JSONL output."`
	DiagFacility         string `default:"MAIN" help:"First argument of PD_GET_DIAG_HIST."`
	DiagRectype          string `default:"ALL" help:"Record type filter."`
	DiagMinImpact        string `default:"" help:"Minimum impact. Empty means all records."`
	DiagOverlapSeconds   int    `default:"10" help:"Re-read window before the watermark, in seconds."`
	DiagMaxRecordsPerRun int    `default:"5000" help:"Maximum diag records per run."`
	DiagMaxFileMb        int    `default:"50" help:"JSONL size in MB at which the file rotates."`
}

// GoString keeps the password out of %#v output.
func (a *argumentList) GoString() string { return a.config().String() }

func (a *argumentList) config() config.Config {
	return config.Config{
		Hostname:                    a.Hostname,
		Port:                        a.Port,
		Database:                    a.Database,
		Username:                    a.Username,
		Password:                    a.Password,
		EnableSSL:                   a.EnableSSL,
		SSLServerCertificate:        a.SSLServerCertificate,
		ConnectTimeout:              a.ConnectTimeout,
		QueryTimeout:                a.QueryTimeout,
		RunTimeout:                  a.RunTimeout,
		CustomMetricsQuery:          a.CustomMetricsQuery,
		CustomMetricsConfig:         a.CustomMetricsConfig,
		CollectTopQueries:           a.CollectTopQueries,
		TopQueriesLimit:             a.TopQueriesLimit,
		CollectLongRunningQueries:   a.CollectLongRunningQueries,
		LongRunningThresholdSeconds: a.LongRunningThresholdSeconds,
		CollectLockWaits:            a.CollectLockWaits,
		StripSQLLiterals:            a.StripSqlLiterals,
		CollectDiagLogs:             a.CollectDiagLogs,
		DiagLogDir:                  resolveDiagLogDir(a.DiagLogDir),
		DiagFacility:                strings.ToUpper(strings.TrimSpace(a.DiagFacility)),
		DiagRecType:                 strings.ToUpper(strings.TrimSpace(a.DiagRectype)),
		DiagMinImpact:               strings.ToUpper(strings.TrimSpace(a.DiagMinImpact)),
		DiagOverlapSeconds:          a.DiagOverlapSeconds,
		DiagMaxRecordsPerRun:        a.DiagMaxRecordsPerRun,
		DiagMaxFileMB:               a.DiagMaxFileMb,
	}
}

func main() {
	os.Exit(run())
}

// run returns the process exit code. Non-zero is reserved for runs that publish nothing.
func run() int {
	var args argumentList
	i, err := integration.New(integrationName, version, integration.Args(&args))
	if err != nil {
		log.Error("%s", err)
		return exitInternalError
	}

	// The SDK uses flag.CommandLine and ignores an environment value it cannot parse.
	if err := checkEnvArgs(flag.CommandLine); err != nil {
		log.Error("invalid configuration: %s", err)
		return exitInvalidConfig
	}

	cfg := args.config()
	if err := cfg.Validate(); err != nil {
		log.Error("invalid configuration: %s", err)
		return exitInvalidConfig
	}
	log.Debug("configuration: %s", cfg)

	// A blocking driver call cannot be cancelled, so the watchdog bounds the whole run.
	var guard publishGuard
	stopWatchdog := newWatchdog(&guard, cfg).start()
	defer stopWatchdog()

	// Diag records are written only on metrics runs, so only those need a usable directory. An
	// unusable one turns diag off for this run; the metrics still publish.
	diagOn := cfg.CollectDiagLogs && args.HasMetrics() && diagDirUsable(cfg)

	if cfg.EnableSSL && cfg.SSLServerCertificate == "" {
		log.Warn("ENABLE_SSL is true but SSL_SERVER_CERTIFICATE is empty: the client falls back to its own trust store")
	}

	e, err := newEntity(i, cfg)
	if err != nil {
		log.Error("create entity: %s", err)
		return exitInternalError
	}

	// Created before any connection attempt, so canConnect=0 is still published on failure.
	ident := identityAttrs(cfg)
	database := e.NewMetricSet(databaseSample, ident...)
	// The identity and the dimension also key the PDELTA baseline, so each pool has its own.
	newBufferpool := func(name string) *metric.Set {
		return e.NewMetricSet(bufferpoolSample,
			append(slices.Clone(ident), attribute.Attr("bufferpoolName", name))...)
	}

	newTablespace := func(name string) *metric.Set {
		return e.NewMetricSet(tablespaceSample,
			append(slices.Clone(ident), attribute.Attr("tablespaceName", name))...)
	}
	// The extras samples share the entity identity and add their dimension attributes.
	newExtra := func(sample string, dims ...attribute.Attribute) *metric.Set {
		return e.NewMetricSet(sample, append(slices.Clone(ident), dims...)...)
	}
	// One store for both users: two FileStores on one path would overwrite each other on Save.
	var state persist.Storer
	if args.HasEvents() || diagOn {
		state = openStateStore(&args, i.Logger(), cfg)
	}
	var tracker func([]queries.Row)
	if args.HasEvents() && state != nil {
		tracker = stateTracker(state, e, ident)
	}

	// Custom samples are metrics. A rejected query is logged here and never stops the others.
	var customQueries []custom.Query
	if args.HasMetrics() {
		customQueries = custom.Load(cfg.CustomMetricsQuery, cfg.CustomMetricsConfig, log.Error)
	}
	newCustom := func(sample string, attrs ...attribute.Attribute) *metric.Set {
		return e.NewMetricSet(sample, attrs...)
	}

	session, err := connection.Connect(connection.BuildDSN(cfg))
	if err != nil {
		log.Warn("cannot connect: %s", connection.Reason(err))
		if err := health.SetCanConnect(database, false); err != nil {
			log.Error("set canConnect: %s", err)
			return exitInternalError
		}
	} else {
		defer func() { _ = session.Close() }()
		sessionOK := true
		if args.HasMetrics() || args.HasEvents() {
			sessionOK = collectCore(sessionFetch(session), target{
				metrics: args.HasMetrics(), database: database, bufferpool: newBufferpool,
				tablespace: newTablespace, tracker: tracker,
			})
		}
		// Extras are metrics. A failure other than a lost connection never changes canConnect.
		if sessionOK && args.HasMetrics() && cfg.ExtrasEnabled() {
			sessionOK = collectExtras(sessionFetch(session), cfg, newExtra)
		}
		if sessionOK && len(customQueries) > 0 {
			sessionOK = collectCustom(sessionLabeledFetch(session), customQueries, newCustom, ident)
		}
		// The diag step runs after the MON_GET_* collectors and never changes canConnect.
		if sessionOK && diagOn {
			collectDiag(newDiagSource(session, cfg), state, cfg)
		}
		// Inventory is read-only: it never changes canConnect, the state store or the baselines.
		if sessionOK && args.HasInventory() {
			collectInventory(sessionFetch(session), e.SetInventoryItem)
		}
		// Last write wins: a connection lost after the ping overwrites the earlier success.
		if err := health.SetCanConnect(database, sessionOK); err != nil {
			log.Error("set canConnect: %s", err)
			return exitInternalError
		}
	}

	// The watchdog may have published already; then it also ends the process, so this never returns.
	if _, err := guard.once(i.Publish); err != nil {
		log.Error("publish: %s", err)
		return exitInternalError
	}
	return exitOK
}

// newEntity creates the reported entity, shared by the normal run and the watchdog document.
func newEntity(i *integration.Integration, cfg config.Config) (*integration.Entity, error) {
	return i.EntityReportedVia(cfg.Endpoint(), cfg.EntityName(), entityNamespace)
}

// identityAttrs are the attributes every sample carries. Never a bare "hostname": the agent injects
// its own.
func identityAttrs(cfg config.Config) []attribute.Attribute {
	return []attribute.Attribute{
		attribute.Attr("db2Hostname", cfg.Hostname),
		attribute.Attr("port", strconv.Itoa(cfg.Port)),
		attribute.Attr("database", strings.ToUpper(cfg.Database)),
	}
}

// openStateStore opens the state file, named after the instance and database. It returns nil, with
// a warning, when the file cannot be opened: then state-change events and diag collection are off
// for this run. SDK v3 has no getter for the store that integration.New creates, so this is a
// second file store.
func openStateStore(args *argumentList, logger log.Logger, cfg config.Config) persist.Storer {
	ttl := args.CacheTTL
	if ttl == 0 {
		ttl = persist.DefaultTTL
	}
	sum := md5.Sum([]byte(cfg.StateKey())) //nolint:gosec // a file name, not security
	path := persist.TmpPath(args.TempDir, stateFilePrefix+hex.EncodeToString(sum[:]))
	store, err := persist.NewFileStore(path, logger, ttl)
	if err != nil {
		log.Warn("open state file %s: %s: state-change events and diag collection are off for this run", path, err)
		return nil
	}
	return store
}

// stateTracker returns the function that compares tablespace states with the previous run, saves
// the state file and then adds an event per change.
func stateTracker(store persist.Storer, e *integration.Entity, ident []attribute.Attribute) func([]queries.Row) {
	attrs := make(map[string]any, len(ident))
	for _, a := range ident {
		attrs[a.Key] = a.Value
	}
	tr := metrics.StateTracker{Store: store, Attrs: attrs}
	return func(rows []queries.Row) { trackStates(tr, e.AddEvent, rows) }
}
