[![Community Project header](https://github.com/newrelic/open-source-office/raw/master/examples/categories/images/Community_Project.png)](https://github.com/newrelic/open-source-office/blob/master/examples/categories/index.md#category-community-project)

# New Relic DB2 Integration

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Requirements](#requirements)
  - [System Requirements](#system-requirements)
  - [New Relic Requirements](#new-relic-requirements)
  - [DB2 Requirements](#db2-requirements)
- [Installation](#installation)
- [Configuration](#configuration)
  - [Integration Config](#integration-config)
  - [Environment Variables](#environment-variables)
  - [Secrets](#secrets)
  - [TLS](#tls)
  - [Password Rules](#password-rules)
  - [Custom Queries](#custom-queries)
  - [Top SQL, Long-Running SQL and Lock Waits](#top-sql-long-running-sql-and-lock-waits)
  - [Diagnostic Logs](#diagnostic-logs)
    - [Mode 1: Remote Collection Through SQL](#mode-1-remote-collection-through-sql)
    - [Mode 2: On-Host Tail of db2diag.log](#mode-2-on-host-tail-of-db2diaglog)
  - [Inventory](#inventory)
  - [State Store](#state-store)
- [Data Collection](#data-collection)
  - [Db2DatabaseSample](#db2databasesample)
  - [Db2BufferpoolSample](#db2bufferpoolsample)
  - [Db2TablespaceSample](#db2tablespacesample)
  - [Db2CustomSample](#db2customsample)
  - [Db2TopQuerySample](#db2topquerysample)
  - [Db2LongRunningQuerySample](#db2longrunningquerysample)
  - [Db2LockWaitSample](#db2lockwaitsample)
  - [Tablespace State-Change Event](#tablespace-state-change-event)
  - [Inventory Items](#inventory-items)
  - [Diagnostic Log Attributes](#diagnostic-log-attributes)
- [Usage](#usage)
  - [NRQL Examples](#nrql-examples)
  - [Dashboard](#dashboard)
- [Troubleshooting](#troubleshooting)
  - [Common Issues](#common-issues)
  - [Debug Mode](#debug-mode)
- [Building](#building)
  - [Prerequisites](#prerequisites)
  - [Steps](#steps)
- [Support](#support)
- [Security](#security)
- [Contributing](#contributing)
- [License](#license)

## Overview

This integration enables remote monitoring of IBM DB2.

## Features

- **Database health**: connectivity (`canConnect`), database status, applications, connections, locks, deadlocks, lock timeouts, transaction log usage, row activity and the age of the last backup
- **Bufferpool metrics**: logical and physical reads and hit percentages per bufferpool, broken down by column, data, index and XDA pages
- **Tablespace metrics**: size, usable space, used space and utilization per tablespace, plus the current tablespace state
- **Tablespace state-change events**: an event whenever a tablespace moves from one state to another, for example from `NORMAL` to `QUIESCED_SHARE`
- **Custom queries**: run your own read-only SQL against the monitored database and publish the result columns as gauges, deltas, rates or attributes
- **Optional extras**: top SQL statements, long-running SQL and current lock waits, each behind its own flag
- **Diagnostic log collection**: `db2diag.log` records are read remotely through `SYSPROC.PD_GET_DIAG_HIST`, or tailed on a Linux DB2 host that runs the agent
- **Inventory**: database manager configuration, database configuration and instance information, so a configuration change is visible in New Relic
- **TLS support**: encrypted connections to DB2 with server name verification
- **Secrets support**: the monitoring password can be supplied through the Infrastructure agent's obfuscated `variables:` mechanism
- **Dashboard**: a template dashboard available via `dashboards/db2-dashboard.json`

## Requirements

### System Requirements

- **Operating System**: Linux on x86-64 (`linux/amd64`) with glibc 2.34 or newer. Supported distributions are RHEL, Rocky Linux and AlmaLinux 9 or later, Ubuntu 22.04 or later, Debian 12 or later, and Amazon Linux 2023.
- **Not supported**: RHEL 8, Ubuntu 20.04, Debian 11 and Amazon Linux 2. They ship an older glibc, and the IBM driver fails to load on them with missing `GLIBC` symbols.
- **IBM Data Server Driver for ODBC and CLI 12.1.4**, installed at `/opt/ibm/clidriver` (so that `libdb2.so.1` is at `/opt/ibm/clidriver/lib`). The integration binary has this location compiled in as its library search path, so no `LD_LIBRARY_PATH` is needed.
- **System libraries**: `libxml2` and `libpam`, plus `libxcrypt-compat` (which provides `libcrypt.so.1`) on the RHEL 9 family and Amazon Linux 2023.
- **Network**: the agent host must reach the DB2 server on its listener port (default `50000`; TLS example `50443`).
- **Architecture**: only `linux/amd64` is built. IBM publishes no Linux arm64 client driver, so arm64 hosts are not supported.

### New Relic Requirements

- A New Relic account
- New Relic Infrastructure Agent installed and configured with a valid ingest key
- Infrastructure agent 1.14.0 or later if you want to store the password with the agent's obfuscation feature (see [Secrets](#secrets))

### DB2 Requirements

- **DB2 LUW server**: Versions 11.5.9 and 12.1.x have been tested. The server can run on any platform that DB2 LUW supports, including AIX, because the integration only needs network access to it.
- **A monitoring user**: A DB2 user is an OS level user on the DB2 server, so create one there (i.e: `monuser`) and give it a password that does not contain a semicolon (`;`).
- **Grants**: The monitoring user needs `CONNECT` on the database and `EXECUTE` on five `SYSPROC.MON_GET_*` functions, or membership of the `SYSMON`, `SQLADM` or `DBADM` authority. All of this is in [`sql/grants.sql`](sql/grants.sql). Without these grants every monitoring query fails with `SQL0551N` / SQLSTATE `42501`.
- **Optional grants**: `SYSPROC.PD_GET_DIAG_HIST` (only for hardened databases that revoked `EXECUTE` from `PUBLIC`) and the three functions behind the optional extras. See [Extras](#extras-top-sql-long-running-sql-and-lock-waits) and [Diagnostic logs](#diagnostic-logs).
- **Monitor switches**: the `MON_GET_*` functions are governed by the `MON_REQ_METRICS` and `MON_OBJ_METRICS` database configuration parameters. Their default value (`BASE`) is sufficient; do not set them to `NONE`.
- One configuration block monitors **one database**. To monitor several databases, add one block per database.

## Installation

1. Install the IBM Data Server Driver for ODBC and CLI 12.1.4 on the agent host:
   ```bash
   curl -fLO https://public.dhe.ibm.com/ibmdl/export/pub/software/data/db2/drivers/odbc_cli/v12.1.4/linuxx64_odbc_cli.tar.gz
   sudo mkdir -p /opt/ibm && sudo tar -xzf linuxx64_odbc_cli.tar.gz -C /opt/ibm
   ls /opt/ibm/clidriver/lib/libdb2.so.1
   ```
   The driver must be readable by the user that runs the Infrastructure agent.

2. Install the system libraries that the driver needs:
   ```bash
   # Ubuntu and Debian
   sudo apt-get install -y libxml2 libpam0g

   # RHEL, Rocky, AlmaLinux 9 and Amazon Linux 2023
   sudo dnf install -y libxml2 pam libxcrypt-compat
   ```

3. Prepare the DB2 side. On the DB2 server, create the monitoring user, then run the grants as the instance owner while connected to the database:
   ```bash
   sudo useradd -m monuser && sudo passwd monuser
   # Edit grants.sql first: replace MONUSER with your monitoring user.
   db2 connect to EXAMPLEDB
   db2 -tvf grants.sql
   ```
   Create the user once per DB2 server. The grants are stored in each database's catalog, so run `db2 connect to <database>` and `db2 -tvf grants.sql` once for every database you want to monitor.

4. Download `nri-db2_linux_<version>_amd64.tar.gz` from the [releases page](https://github.com/newrelic/nri-db2/releases) and unpack it:
   ```bash
   tar -xzf nri-db2_linux_<version>_amd64.tar.gz
   ```
   The archive contains the `nri-db2` binary, `LICENSE`, `README.md`, the sample files under `config/` and `sql/grants.sql`.

5. Copy the binary to the Infrastructure agent's custom integrations directory:
   ```bash
   sudo cp nri-db2 /var/db/newrelic-infra/custom-integrations
   sudo chmod +x /var/db/newrelic-infra/custom-integrations/nri-db2
   ```

6. Copy the sample configuration and edit it:
   ```bash
   sudo cp config/db2-config.yml.sample /etc/newrelic-infra/integrations.d/db2-config.yml
   # Optional: custom queries
   sudo mkdir -p /etc/newrelic-infra/nri-db2
   sudo cp config/db2-custom-query.yml.sample /etc/newrelic-infra/nri-db2/db2-custom-query.yml
   ```
   Keep the custom query file out of `integrations.d`: the agent treats every `.yml` file in that directory as an integration config and logs a warning for a file that has no `integrations` field.
   Set `HOSTNAME`, `DATABASE`, `USERNAME` and `PASSWORD` at a minimum. See [Configuration](#configuration).

7. Optional: to collect diagnostic logs, copy the matching log config example without the `.example` suffix. Use the first file for [Mode 1](#mode-1-remote-collection-through-sql) (remote collection, recommended) or the three config files for [Mode 2](#mode-2-on-host-tail-of-db2diaglog) (agent on the DB2 host itself). Do not use both modes for the same DB2 instance.
   ```bash
   # Mode 1
   sudo cp config/db2-diag-jsonl-log.yml.example /etc/newrelic-infra/logging.d/db2-diag-jsonl-log.yml

   # Mode 2
   sudo cp config/db2-log.yml.example        /etc/newrelic-infra/logging.d/db2-log.yml
   sudo cp config/db2-fluentbit.conf.example /etc/newrelic-infra/logging.d/db2-fluentbit.conf
   sudo cp config/db2-parsers.conf.example   /etc/newrelic-infra/logging.d/db2-parsers.conf
   ```

8. Restart the Infrastructure agent:
   ```bash
   sudo systemctl restart newrelic-infra
   ```

9. After one or two intervals, check that data arrives in New Relic (see [Usage](#usage)).

## Configuration

The integration is configured in the Infrastructure agent's integration config file, `/etc/newrelic-infra/integrations.d/db2-config.yml`, using environment variables in the `env:` map of each `integrations:` entry. Custom queries live in a second, optional file. Samples of both are in the `config/` directory of the release archive.

### Integration Config

Each `integrations:` entry monitors one database. This is an abbreviated version of `config/db2-config.yml.sample`:

```yaml
integrations:
  # Plain TCP.
  - name: nri-db2
    env:
      HOSTNAME: db2-prod-01.example.com     # always set explicitly; there is no default
      # PORT: 50000
      DATABASE: SALESDB
      USERNAME: monuser
      PASSWORD: '<password>'                # must not contain ';'
      # or ${db2MonitorPassword} with the variables: block above
      # CONNECT_TIMEOUT: 10
      # QUERY_TIMEOUT: 30
      # RUN_TIMEOUT: 100

      # Custom queries (optional).
      # CUSTOM_METRICS_CONFIG: /etc/newrelic-infra/nri-db2/db2-custom-query.yml

      # Extra collections, all off by default. Each needs a GRANT EXECUTE from sql/grants.sql.
      # COLLECT_TOP_QUERIES: true
      # TOP_QUERIES_LIMIT: 20
      # COLLECT_LONG_RUNNING_QUERIES: true
      # LONG_RUNNING_THRESHOLD_SECONDS: 60
      # COLLECT_LOCK_WAITS: true

      # db2diag.log collection through PD_GET_DIAG_HIST (opt-in). Also copy
      # db2-diag-jsonl-log.yml.example to /etc/newrelic-infra/logging.d/db2-diag-jsonl-log.yml.
      # COLLECT_DIAG_LOGS: true
      # DIAG_LOG_DIR: /var/log/nri-db2
    interval: 30s
    # timeout: 120s                         # the agent stops a run that lasts longer (default 120s); see Troubleshooting
    labels:                                 # custom attributes added to every sample
      env: production
      team: dba
    inventory_source: config/db2

  # TLS. HOSTNAME must match the server certificate.
  - name: nri-db2
    env:
      HOSTNAME: db2-prod-02.example.com
      PORT: 50443
      DATABASE: SALESDB
      USERNAME: monuser
      PASSWORD: '<password>'                # or ${db2MonitorPassword}
      ENABLE_SSL: true                      # required whenever SSL_SERVER_CERTIFICATE is set
      SSL_SERVER_CERTIFICATE: /etc/newrelic-infra/db2/db2-prod-02.arm
    interval: 30s
    labels:
      env: production
    inventory_source: config/db2
```

Notes:

- `HOSTNAME` has no default and must always be set explicitly in the `env:` block.
- `interval` is the polling interval. Counters are reported as the change since the previous run, so their size scales with the interval (see [Data Collection](#data-collection)). Keep the interval well below the state cache lifetime of 6 minutes (see [State store](#state-store)).
- `timeout` is optional and is an agent setting, not an integration variable. The agent stops a run that lasts longer than this (default `120s`), and a stopped run publishes nothing. The integration's own `RUN_TIMEOUT` (default 100 seconds) ends a slow run first and publishes `canConnect = 0`. See [Troubleshooting](#troubleshooting) item 12.
- `labels:` add custom attributes to every sample. Use them for the tags you would otherwise attach per instance, for example environment or team.
- `inventory_source: config/db2` is the inventory path prefix.

### Environment Variables

These are the integration's own settings. All of them are set in the `env:` map.

| Variable | Default | Description |
|----------|---------|-------------|
| `HOSTNAME` | none (required) | DB2 server host name. With `ENABLE_SSL` it must match the name in the server certificate. |
| `PORT` | `50000` | DB2 listener port (1-65535). |
| `DATABASE` | none (required) | Database name (alias). One block monitors one database. |
| `USERNAME` | none (required) | Monitoring user. Needs the grants in `sql/grants.sql`, or `SYSMON`, `SQLADM` or `DBADM`. |
| `PASSWORD` | none (required) | Password for the monitoring user. Must not contain `;`. Supply it through the agent `variables:` block (see [Secrets](#secrets)). |
| `ENABLE_SSL` | `false` | Use TLS to the server. |
| `SSL_SERVER_CERTIFICATE` | empty | Path to the server certificate (`.arm`) that the client trusts. Requires `ENABLE_SSL: true`. |
| `CONNECT_TIMEOUT` | `10` | Seconds to wait when establishing the connection (minimum 1). |
| `QUERY_TIMEOUT` | `30` | Per-statement cancel timeout in seconds (minimum 1). It does not interrupt every statement type. The whole run is bounded by `RUN_TIMEOUT`; see [Troubleshooting](#troubleshooting) item 12. |
| `RUN_TIMEOUT` | `100` | Overall limit for one run, in seconds. `0` disables it; otherwise the minimum is 1. If a run exceeds it, the integration publishes a `Db2DatabaseSample` with `canConnect = 0` and exits with code 0, because the agent kills an integration that runs longer than its own `timeout` (default 120 seconds) and a killed run publishes nothing. Keep it below the agent `timeout`. |
| `CUSTOM_METRICS_QUERY` | empty | One inline custom query. Result columns become metrics on `Db2CustomSample`. |
| `CUSTOM_METRICS_CONFIG` | empty | Path to a YAML file of custom queries. May be combined with `CUSTOM_METRICS_QUERY`. |
| `COLLECT_TOP_QUERIES` | `false` | Enable top SQL collection (`Db2TopQuerySample`). |
| `TOP_QUERIES_LIMIT` | `20` | Maximum number of statements reported per run (minimum 1). |
| `COLLECT_LONG_RUNNING_QUERIES` | `false` | Enable long-running SQL collection (`Db2LongRunningQuerySample`). |
| `LONG_RUNNING_THRESHOLD_SECONDS` | `60` | Report activities that have been running longer than this many seconds (minimum 1). |
| `COLLECT_LOCK_WAITS` | `false` | Enable lock-wait collection (`Db2LockWaitSample`). |
| `COLLECT_DIAG_LOGS` | `false` | Collect `db2diag.log` records through `SYSPROC.PD_GET_DIAG_HIST` and write them as JSONL files. |
| `DIAG_LOG_DIR` | `/var/log/nri-db2` | Directory for the diagnostic JSONL files. Should be creatable and writable when `COLLECT_DIAG_LOGS` is true. If it is not (or the path is invalid), the integration logs a warning and skips diagnostic collection for that run; the metrics and `canConnect` are still published. |
| `DIAG_FACILITY` | `MAIN` | Diagnostic facility: `MAIN`, `OPTSTATS` or `ALL`. |
| `DIAG_RECTYPE` | `ALL` | Record type filter: `ALL`, `D` (all diagnostic records), `E` (all event records), `DI`, `DX`, `EI` or `EX`, or a combination joined with `+`, for example `E+D`. |
| `DIAG_MIN_IMPACT` | empty | Minimum impact: empty (keep every record), `UNLIKELY`, `POTENTIAL`, `IMMEDIATE` or `CRITICAL` (`NONE` is also accepted). Any non-empty value, including `NONE`, drops records that have no impact value, which includes event records (measured on Db2 11.5.9 and 12.1.5). |
| `DIAG_OVERLAP_SECONDS` | `10` | Seconds before the last record that each run re-reads. Duplicates are removed. Minimum 0. |
| `DIAG_MAX_RECORDS_PER_RUN` | `5000` | Maximum number of new diagnostic records written per run (minimum 1). Db2 is asked for only that many rows (plus the records of the overlap window that are already written), so a backlog is read in slices and drains over several runs. A slice that holds no new record falls back to reading the whole window once. |
| `DIAG_MAX_FILE_MB` | `50` | Size in MB at which the JSONL file rotates (minimum 1). |

Standard arguments of the New Relic integrations SDK are also accepted, as environment variables or as command-line flags (the flag name is the lower-case variable name). A flag overrides the environment variable. You rarely need to set these:

| Variable | Default | Description |
|----------|---------|-------------|
| `METRICS` | `false` | Publish metrics. If none of `METRICS`, `INVENTORY` and `EVENTS` is set, all three are published. |
| `INVENTORY` | `false` | Publish inventory. |
| `EVENTS` | `false` | Publish events (the tablespace state-change event). |
| `VERBOSE` | `false` | Debug logging to stderr. |
| `PRETTY` | `false` | Indented JSON output (useful when running by hand). |
| `METADATA` | `false` | Add the customer-defined attributes from the agent's `labels:` to the samples. |
| `TEMP_DIR` | empty | Directory for the integration's state files. Defaults to `nr-integrations` under the system temporary directory. |
| `CACHE_TTL` | `6m` | How long stored state (counter baselines, tablespace states, diagnostic watermark) stays valid. See [State store](#state-store). |

The IBM client also reads its own environment variables. One matters here:

| Variable | Default | Description |
|----------|---------|-------------|
| `DB2_MAX_CLIENT_CONNRETRIES` | `0` | IBM client setting, not an integration key. Without it, the 12.1.4 client takes about 5 seconds longer to fail a connection. The integration sets it to `0` before the first connection unless you have already set it in the process environment, in which case your value is kept. |

**Invalid values are rejected.** An environment variable that cannot be parsed (a boolean, an integer or a duration) is a startup error: the integration logs which variable is wrong (never its value for `USERNAME` and `PASSWORD`) and exits with code 2, instead of silently ignoring it. Booleans must be a Go `ParseBool` value: `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false` or `False`. `yes`, `no`, `on` and `off` are rejected. In YAML, quote booleans (`ENABLE_SSL: "true"`) so that the agent passes the literal text and not a value converted by the YAML parser.

Changing any argument between runs (including adding a flag) starts a new state store, which resets the counter baselines. Keep the configuration stable.

### Secrets

It is recommended to encrypt the password in the configuration file. With Infrastructure agent 1.14.0 or later, obfuscate it and reference it as a variable.

1. Create the obfuscated value with the New Relic CLI:
   ```bash
   KEY=$(openssl rand -hex 8) # alternatively, choose your own key
   newrelic agent config obfuscate --value '<password>' --key '<key>'
   ```
   The command prints JSON, for example `{"obfuscatedValue": "<secret>"}`. The secret is the `obfuscatedValue` string.
2. In `db2-config.yml`, uncomment the `variables:` block (it ships commented out so that an unedited copy does not log a decoding error), fill in the key you passed and the secret that the command printed, then set `PASSWORD: ${db2MonitorPassword}`:
   ```yaml
   variables:
     db2MonitorPassword:
       obfuscated:
         key: <key passed to --key>
         secret: <obfuscated password printed by the command>

   integrations:
     - name: nri-db2
       env:
         PASSWORD: ${db2MonitorPassword}
   ```

To download the CLI (if not already present):

```bash
curl -Ls https://download.newrelic.com/install/newrelic-cli/scripts/install.sh | bash
```

The agent resolves the variable before it starts the integration.

For other available secret options, [see docs](https://docs.newrelic.com/docs/infrastructure/host-integrations/installation/secrets-management/).

### TLS

To encrypt the connection, set `ENABLE_SSL: true` and point `SSL_SERVER_CERTIFICATE` at the server certificate (an `.arm` file exported from the DB2 server's key database):

```yaml
env:
  HOSTNAME: db2-prod-02.example.com
  PORT: 50443
  ENABLE_SSL: true
  SSL_SERVER_CERTIFICATE: /etc/newrelic-infra/db2/db2-prod-02.arm
```

- The DB2 server must already listen for TLS connections on the configured port.
- The IBM 12.1.4 client verifies the server name. `HOSTNAME` must be the name that appears in the certificate. Connecting by IP address, or by any other name, fails with `SQL20576N`.
- Setting `SSL_SERVER_CERTIFICATE` without `ENABLE_SSL: true` is a startup error. The integration refuses to run rather than ignore the certificate and connect in plaintext.
- `ENABLE_SSL: true` without a certificate is allowed and logs a warning. A server with a self-signed certificate is then likely to fail with `SQL30081N`.
- Keep the IBM client at 12.1.4 or later. Older clients do not verify the server name.

### Password Rules

A semicolon (`;`) ends a value in the connection string, and quoting it does not work. The integration therefore rejects a `PASSWORD` that contains `;` at startup, with a message that never includes the password. The same applies to `HOSTNAME`, `DATABASE`, `USERNAME` and `SSL_SERVER_CERTIFICATE`. Curly braces (`{` and `}`) in a password are fine. If your password contains a semicolon, change it for the monitoring user.

### Custom Queries

Custom queries let you publish your own read-only SQL results. Set `CUSTOM_METRICS_CONFIG` to a YAML file (start from `config/db2-custom-query.yml.sample`), or `CUSTOM_METRICS_QUERY` to a single inline statement. If you set both, both run. Each query runs once per interval and publishes one sample per result row.

```yaml
queries:
  - name: bufferpool_data_reads
    query: >-
      SELECT bp_name AS "bufferpoolName", pool_data_l_reads AS "dataLogicalReads"
      FROM TABLE(MON_GET_BUFFERPOOL(NULL, -1))
    metric_types:
      dataLogicalReads: gauge
    sample_name: Db2CustomSample

  - name: tablespace_files_closed
    query: >-
      SELECT tbsp_name AS "tablespaceName", files_closed AS "filesClosed"
      FROM TABLE(MON_GET_TABLESPACE(NULL, -1))
    metric_types:
      filesClosed: delta
```

| Key | Required | Description |
|-----|----------|-------------|
| `name` | No | Value of the `query` attribute on the sample. Defaults to `custom<N>` for file queries and `inline` for `CUSTOM_METRICS_QUERY`. Names must be unique. |
| `query` | Yes | One `SELECT`, `WITH` or `VALUES` statement. Other statement types are refused and not sent. A single trailing `;` is removed. |
| `metric_types` | No | Maps a result column label to `gauge`, `delta`, `rate` or `attribute`. Labels are case-sensitive. |
| `sample_name` | No | Event type of the published sample. Defaults to `Db2CustomSample`. |

Rules and behavior:

- **Column labels become attribute names.** DB2 upper-cases an unquoted alias, so alias columns in double quotes to keep the case (`AS "dataLogicalReads"`).
- **Default types.** Without `metric_types`, numeric columns (`BIGINT`, `SMALLINT`, `INTEGER`, `REAL`, `DOUBLE`, `DECIMAL`, `DECFLOAT`) are gauges, and string and timestamp columns (`VARCHAR`, `CHAR`, `TIMESTAMP`, published as RFC 3339 text) are attributes. `NULL` values are skipped.
- **`delta`** publishes the difference since the previous run and drops a negative difference (a counter reset). The first run of a row publishes `0`. **`rate`** publishes the difference per second and can go negative after a counter reset, so prefer `delta` for counters.
- **Row identity.** For `delta` and `rate`, every attribute column of a row identifies that row's baseline. A query that uses `delta` or `rate` must therefore select at least one stable string column (such as `tablespaceName`); otherwise that query is skipped on every run and the reason is logged under its name. An attribute whose value changes between runs (statement text, a timestamp) starts a new baseline each run, so the delta is always `0`.
- **Not supported**: per-query static tags (use agent-level `labels:`), a `count` column type (aggregate with `GROUP BY` in the SQL), and the SDK types `pdelta` and `prate`.
- **Reserved labels** are skipped with a warning. They are compared case-insensitively: `event_type`, `entityKey`, `entityName`, `reportingEndpoint`, `db2Hostname`, `port`, `database`, `query`, `hostname`, `host`, `fullHostname`, `agentName`, `agentVersion`, `displayName`, `entityGuid`, `entityId`, `integrationName`, `integrationVersion`, `reportingAgent`, `externalKey`, `timestamp`, `eventType` and `eventId`.
- **Read-only SQL.** Custom SQL that contains `FINAL TABLE`, `NEW TABLE` or `OLD TABLE` (data-change table references, which can write data) is rejected when the queries are loaded.
- **Failure isolation.** A query that fails (typo, missing privilege, timeout) is logged by name and skipped. The standard samples and the other queries are not affected.
- **Row cap.** At most 1000 rows of one query are published per run. When a query returns more, the first 1000 are published and a warning names the query; the rest are not read. Narrow the query with a `WHERE` clause, and add an `ORDER BY` so the same rows stay inside the cap, because a `delta` or `rate` metric restarts its baseline when the rows inside the cap change. The cap is fixed and has no setting.
- Every custom sample carries the identity attributes `db2Hostname`, `port` and `database`, plus `query`.

### Top SQL, Long-Running SQL and Lock Waits

Three optional collectors are available and disabled by default. Each has its own flag and needs its own grant, because nobody (not even `PUBLIC`) holds `EXECUTE` on these functions by default. Without the grant, the monitoring user gets `SQL0551N` / SQLSTATE `42501`; the integration logs one warning for that collector and carries on with everything else.

| Flag | DB2 function | Grant (run as the instance owner) | NR Sample |
|------|--------------|-----------------------------------|--------|
| `COLLECT_TOP_QUERIES` | `MON_GET_PKG_CACHE_STMT` | `GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_PKG_CACHE_STMT TO USER MONUSER;` | `Db2TopQuerySample` |
| `COLLECT_LONG_RUNNING_QUERIES` | `MON_GET_ACTIVITY` | `GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_ACTIVITY TO USER MONUSER;` | `Db2LongRunningQuerySample` |
| `COLLECT_LOCK_WAITS` | `MON_GET_APPL_LOCKWAIT` | `GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_APPL_LOCKWAIT TO USER MONUSER;` | `Db2LockWaitSample` |

The grants are present, commented out, in `sql/grants.sql`. Uncomment the ones you need.

- **Top SQL** returns the statements with the highest total activity time (`TOTAL_ACT_TIME`) from the package cache, up to `TOP_QUERIES_LIMIT`. Values are cumulative since the statement's section was inserted into the package cache, and Db2 adds an execution to them when it finishes, so a statement that is still executing is not counted yet. They are published as gauges. If activity metrics are off (`MON_ACT_METRICS` is `NONE`), the time and row values stay at 0. The per-execution averages `avgActTime` and `avgCpuTime` are computed by the integration as the total divided by `numExecutions`; `numExecutions` counts every execution, including any that ran while activity metrics were off, so the averages can read low in that case. The integration's own monitoring statements are excluded: each one starts with the comment `/* nri-db2 */`, and that prefix is the only thing hidden, so your statements, including your custom queries and other tools' `MON_GET_*` queries, stay visible. The ranking happens first and the exclusion only checks the top `TOP_QUERIES_LIMIT + 30` entries, so the cost does not grow with the total size of the package cache. If more than 30 of those entries are the integration's own statements, fewer than `TOP_QUERIES_LIMIT` statements are reported. Db2 keeps the package cache only while the database is active: if the last connection closes and nothing activated the database (`ACTIVATE DATABASE`), the cache is discarded and Top SQL reports nothing until statements run again.
- **Long-running SQL** returns activities that started more than `LONG_RUNNING_THRESHOLD_SECONDS` ago and have not completed, oldest first, up to 100 per run. An open activity can also be `IDLE` (for example, waiting for the client to fetch more rows), so check `activityState`. Activities that are queued and have no start time are not reported. The integration looks at the DB2 server once per interval, so a statement is reported only if a run happens while it is above the threshold. A statement that crosses the threshold and finishes before the next run is missed. For example, a 30 second statement with a threshold of 20 seconds and a 15 second interval is above the threshold for only 10 seconds, so a run can fall outside that window. Choose a threshold well below the duration you care about, or a shorter interval. A statement that is blocked on a lock is an open activity, so it is reported here as well as in a lock-wait sample.
- **Lock waits** returns one sample per application currently waiting for a lock, up to 100 per run, with the holder's application handle.
- **Multiple members.** The three functions are called for all members. Top SQL and long-running SQL samples carry a `member` dimension, so the same `executableId` or activity on two members stays two samples. Lock-wait samples are keyed by the waiting `applicationHandle` only; an application with several waiting agents (parallel queries or several members) could share one sample. Multi-member databases have not been tested.

**Privacy note:** `statementText` is the raw SQL text, cut to 4094 characters. Statements can contain literal values such as names, numbers or other business data, and that text is sent to New Relic. Enable `COLLECT_TOP_QUERIES` and `COLLECT_LONG_RUNNING_QUERIES` only if that is acceptable for your data.

### Diagnostic Logs

DB2 writes its diagnostic log (`db2diag.log`) on the DB2 server. There are two ways to collect it. Use only one of them for a given DB2 instance, because both set `logtype: db2diag` and overlapping sources produce duplicate messages.

#### Mode 1: Remote Collection Through SQL

The integration reads the diagnostic history through `SYSPROC.PD_GET_DIAG_HIST` on the same connection as the metrics, so nothing is installed on the DB2 server. This is the mode to use for AIX DB2 servers or any DB2 host that does not run the Infrastructure agent.

1. Set `COLLECT_DIAG_LOGS: true` in the integration's `env:` block.
2. Copy the log example so the agent tails the files the integration writes:
   ```bash
   sudo cp config/db2-diag-jsonl-log.yml.example /etc/newrelic-infra/logging.d/db2-diag-jsonl-log.yml
   ```
   If you change `DIAG_LOG_DIR`, change the `file:` path in that file to match.
3. Restart the Infrastructure agent.

How it works:

- Records are written, one JSON object per line, to `DIAG_LOG_DIR/db2diag-<host>_<port>_<db>.jsonl` (default directory `/var/log/nri-db2`). The directory is created with mode `0750` if it is missing, owned by the user that runs the agent, and a run that cannot create or write it (or is given an invalid path) logs a warning and skips diagnostic collection for that run, while the metrics and `canConnect` are still published. If the agent runs as a non-root user, or the log forwarder runs as a different user, create the directory first and give it to that user, for example `sudo install -d -m 0750 -o <agent user> /var/log/nri-db2`. The agent's log forwarder must be able to read the files.
- Files rotate by size: a run that has records to write and finds the file at or above `DIAG_MAX_FILE_MB` (default 50) first renames it to `<same>.1`, replacing any previous `.1`. A file can therefore exceed the limit by one run's output. At most two generations exist per instance.
- The `message` of each log event is the full text of the record as `PD_GET_DIAG_HIST` returns it (its `FULLREC` column). For most rows this is the `db2diag.log` record. With the default `DIAG_FACILITY` of `MAIN`, rows can also come from the administration notification log and have a different layout. The records arrive in New Relic as `Log` events with `logtype` set to `db2diag`.
- The first run only records a starting point and writes nothing: there is no backfill of history. Later runs read from the last record seen, minus `DIAG_OVERLAP_SECONDS`, and drop duplicates. Delivery is at-least-once.
- In a non-restrictive database, `EXECUTE` on `PD_GET_DIAG_HIST` is granted to `PUBLIC`. If the database was created `RESTRICTIVE`, or `PUBLIC` was revoked, uncomment this line in `sql/grants.sql` and run it:
  ```sql
  GRANT EXECUTE ON FUNCTION SYSPROC.PD_GET_DIAG_HIST TO USER MONUSER;
  ```
- `PD_GET_DIAG_HIST` needs a temporary table space with a page size of 8K or larger in the database. Without one it fails with `SQL1585N`.
- The diagnostic log belongs to the DB2 instance, not to a database. If two configuration blocks point at two databases of the same instance, enable `COLLECT_DIAG_LOGS` on only one of them, otherwise the same records are shipped twice.
- **Time zones and the server clock:** record times are converted to UTC with the offset Db2 stores in each record, including half-hour zones and zones from UTC+13 to UTC+14, so the `timestamp` of a log line is the real instant. The read position is a wall-clock time on the Db2 server, so it never moves ahead of the server clock. When the server clock goes back (for example a daylight saving fall-back) or a process writes a record stamped in the future, collection continues from the server clock. After a fall-back, the records written in the repeated hour are read again, so up to about an hour of records can appear twice in the log file, once per transition, and the held keys of that hour stay in the state file until the clock passes them.
- **Masked hexdump bytes:** New Relic can mask digit groups in a log `message` that look like card numbers. In a DB2 record this shows up inside a `Hexdump` line, where a group such as `5354 4152 5420 2020` is replaced by `X` characters. The rest of the record is unchanged, and the JSONL file on disk keeps the original text.
- **Data governance:** diagnostic records can contain user IDs and client IP addresses (for example failed-login messages). Enable this only if you are comfortable sending that data to New Relic, and restrict access to `DIAG_LOG_DIR` to the agent user.

#### Mode 2: On-Host Tail of db2diag.log

When the Infrastructure agent runs on the Linux DB2 host itself, it can tail `db2diag.log` directly. Copy the three example files shown in [Installation](#installation) step 7, then edit the `Path` in `db2-fluentbit.conf` to your instance's `DIAGPATH` (usually `<instance home>/sqllib/db2dump/DIAG0000/db2diag.log`).

- `db2-parsers.conf` defines a multi-line parser that starts a new record only at the full header timestamp, so records that contain timestamp-like data lines are not split. Keep `flush_timeout` at 2000 ms or more.
- `logtype: db2diag` is set by the `record_modifier` filter in `db2-fluentbit.conf`, and `filePath` holds the file the record came from.
- The `timestamp` of each record is the time the agent read it, not the time in the record header. They normally differ by a second or two, but after an agent outage the backlog is stamped with the catch-up time. The header time is always the first token of `message`.
- Records arrive with the full text in `message`. They are not parsed into attributes unless you add a New Relic log parsing rule for `logtype = 'db2diag'`.
- If the `DIAGSIZE` database manager parameter is nonzero, Db2 writes a series of rotating `db2diag.N.log` files instead of one `db2diag.log`; point `Path` at the files you want to tail. How the agent's tail handles that rotation has not been tested.
- Mode 2 needs no `COLLECT_DIAG_LOGS` setting.

### Inventory

With `inventory_source: config/db2`, the integration publishes configuration inventory for the `db2-instance` entity from `SYSIBMADM.DBMCFG`, `SYSIBMADM.DBCFG` and `SYSIBMADM.ENV_INST_INFO`. No extra grant is needed in a non-restrictive database, where `SELECT` on these views is granted to `PUBLIC`. In a database created `RESTRICTIVE`, grant `SELECT` on the three views to the monitoring user. Inventory is published when the run publishes inventory, which is the default when no `METRICS`, `INVENTORY` or `EVENTS` setting is present. See [Inventory items](#inventory-items) for the item names.

### State Store

Between runs the integration keeps a small state file in `TEMP_DIR` (counter baselines, the last known tablespace states and the diagnostic-log watermark). Stored state expires after `CACHE_TTL`, which defaults to 6 minutes. This has three consequences:

- After an outage longer than `CACHE_TTL` (the agent or integration not running, or DB2 unreachable), the stored state is gone. The counters (`PDELTA` metrics) restart their baseline and report `0` on the first successful run, and the diagnostic-log watermark is lost, so records written during the outage are not collected. `CACHE_TTL` is a standard argument of the integrations SDK (default `6m`), and you can raise it, for example to `1h`, to ride out longer outages. It applies to every stored value.
- If your polling `interval` is 6 minutes or longer, every run behaves like a first run. Keep the interval shorter, or raise `CACHE_TTL` (it affects every stored value).
- Changing any integration argument starts a new baseline.

## Data Collection

Each `integrations:` block is reported as one **remote entity**:

| Item | Value |
|------|-------|
| Entity type (namespace) | `db2-instance` |
| Entity name | `<HOSTNAME>/<DATABASE>` with the database name **upper-cased**, for example `db2-prod-01.example.com/SALESDB`. In NRQL, the agent sets the `entityName` attribute of the samples to `db2-instance:<HOSTNAME>/<DATABASE>`, with the type prefix; the diagnostic `Log` records carry it without the prefix. |
| Reporting endpoint | `<HOSTNAME>:<PORT>` |

Every sample carries three identity attributes, so you can filter and facet across all sample types:

| Attribute | Type | Description |
|-----------|------|-------------|
| `db2Hostname` | string | The `HOSTNAME` you configured. The integration never uses a bare `hostname` attribute, because the Infrastructure agent adds its own. |
| `port` | string | The `PORT` you configured. |
| `database` | string | The `DATABASE` you configured, upper-cased. |

The integration is **short-lived**: on each interval the agent starts it, it opens one connection, runs all collectors, publishes the result and exits. If a connection-class error occurs, it reconnects once within the run. Otherwise the next interval reconnects. When it cannot connect, it still publishes a `Db2DatabaseSample` with `canConnect` set to `0` and exits normally; it exits with an error only when the configuration is invalid or nothing could be published.

**Counters.** Attributes marked `delta` below are cumulative DB2 counters. They are reported as the change since the previous run, and a counter reset (for example after the database is reactivated) produces no value for that run instead of a negative number. The first sample after the integration starts, after the stored state expires, or after a configuration change reports `0` for every delta attribute. Ignore the first sample in alerts and charts.

**Types used in the tables:** `gauge` is a point-in-time value, `delta` is the per-interval change, `attribute` is a string.

### Db2DatabaseSample

#### Health

| Attribute | Type | Description |
|-----------|------|-------------|
| `canConnect` | gauge | `1` if the connection and queries worked in this run, `0` if the integration could not connect. If it cannot connect, no other metric is present. A connection with missing grants still reports `1` but has no metrics, so alert on missing data as well. |
| `databaseStatus` | attribute | Raw `DB_STATUS` from DB2. Db2 documents `ACTIVE`, `QUIESCE_PEND`, `QUIESCED`, `ROLLFWD`, `ACTIVE_STANDBY` and `STANDBY`. Absent if DB2 returns no value. |
| `databaseStatusCode` | gauge | `0` for `ACTIVE`, `ACTIVE_STANDBY` and `STANDBY`; `1` for `QUIESCE_PEND` and `ROLLFWD`; `2` for `QUIESCED`; `3` for any other value. These codes are this integration's severity scale (0 OK, 1 warning, 2 critical, 3 unknown), not Db2's own numeric status constants. A quiesced database normally rejects the monitoring user, which is reported as `canConnect = 0` instead. |

#### Metrics

| Attribute | Type | Description |
|-----------|------|-------------|
| `applicationActive` | gauge | Number of applications currently connected to the database. |
| `applicationExecuting` | gauge | Number of applications currently executing a request in the database manager. |
| `backupLatest` | gauge | Seconds since the last backup. `-1` means Db2 reports no completed backup (`LAST_BACKUP` is null); treat it as "never", not as a short time. |
| `connectionActive` | gauge | Number of connections currently open to the DB2 instance, local and remote combined (`TOTAL_CONNECTIONS` from `MON_GET_INSTANCE`). |
| `connectionMax` | gauge | High-water mark of concurrent connections since database activation. |
| `connectionTotal` | delta | New connections made to the database. |
| `lockActive` | gauge | Number of locks currently held. |
| `lockDead` | delta | Deadlocks that occurred. |
| `lockPages` | gauge | Lock list memory in use, in 4 KB pages (the unit of the `LOCKLIST` parameter). Db2 reports `LOCK_LIST_IN_USE` in bytes; the integration divides by 4096. |
| `lockTimeouts` | delta | Lock requests that timed out. |
| `lockWait` | gauge | Average lock wait time in milliseconds, calculated as total lock wait time divided by the number of lock waits since database activation (`0` when there were no waits). It is a lifetime average and moves slowly. |
| `lockWaiting` | gauge | Number of agents (application requests) currently waiting on a lock (`NUM_LOCKS_WAITING`, Db2 element `locks_waiting`). This counts waiters, not lock objects. |
| `logAvailable` | gauge | Active log space not used by uncommitted transactions, in 4 KB pages (Db2 reports bytes; the integration divides by 4096). `-1` means the log space is unlimited (infinite logging, `LOGSECOND` set to `-1`). |
| `logReads` | delta | Number of log pages read. |
| `logUsed` | gauge | Active log space currently in use, in 4 KB pages (Db2 reports bytes; the integration divides by 4096). |
| `logUtilized` | gauge | Percentage of the transaction log in use, calculated as used / (used + available) x 100, so it is always between 0 and 100. `0` when log space is unlimited. Not published when used + available is `0` or when either value is missing. |
| `logWrites` | delta | Number of log pages written. |
| `rowModifiedTotal` | delta | Rows inserted, updated or deleted. |
| `rowReadsTotal` | delta | Rows read (including rows read internally to find the requested rows). |
| `rowReturnedTotal` | delta | Rows returned to applications. |

### Db2BufferpoolSample

One sample per bufferpool, with the extra dimension `bufferpoolName`. The counters come from `MON_GET_BUFFERPOOL` for the connected database member and accumulate from database activation; `delta` metrics report the change per interval. The reads of each page type include the matching temporary table space reads (`POOL_TEMP_*`). Hit percentages are cumulative since database activation, not per interval, so on a long-running database they move slowly. They are 0 to 100 and are calculated from the pages found in the local bufferpool (excluding asynchronous prefetch) divided by logical reads (including temporary-table reads). They are `0` when the pool had no logical reads. The five `bufferpoolGroup*` metrics are the exception: they are omitted, not `0`, when there were no group bufferpool reads.

| Attribute | Type | Description |
|-----------|------|-------------|
| `bufferpoolName` | attribute | Bufferpool name, for example `IBMDEFAULTBP`. |
| `bufferpoolHitPercent` | gauge | Overall hit percentage across column, data, index and XDA pages. |
| `bufferpoolReadsLogical` | delta | Logical page reads (pages requested from the bufferpool), summed over column, data, index and XDA pages. Includes temporary table spaces. |
| `bufferpoolReadsPhysical` | delta | Physical page reads (pages read in from the table space containers, by agents and prefetchers), summed over column, data, index and XDA pages. Includes temporary table spaces. |
| `bufferpoolReadsTotal` | delta | Logical plus physical reads, summed over all page types. This is a convenience sum, not a Db2 monitor element. Pages read from disk are counted in both terms, so use `bufferpoolReadsLogical` for the number of page requests. |
| `bufferpoolColumnHitPercent` | gauge | Hit percentage for column-organized table pages. |
| `bufferpoolColumnReadsLogical` | delta | Logical reads of column-organized table pages, including temporary table spaces. |
| `bufferpoolColumnReadsPhysical` | delta | Physical reads of column-organized table pages, including temporary table spaces. |
| `bufferpoolColumnReadsTotal` | delta | Logical plus physical reads of column-organized table pages. A convenience sum, not a Db2 monitor element; pages read from disk are counted in both terms. |
| `bufferpoolDataHitPercent` | gauge | Hit percentage for data pages. |
| `bufferpoolDataReadsLogical` | delta | Logical reads of data pages, including temporary table spaces. |
| `bufferpoolDataReadsPhysical` | delta | Physical reads of data pages, including temporary table spaces. |
| `bufferpoolDataReadsTotal` | delta | Logical plus physical reads of data pages. A convenience sum, not a Db2 monitor element; pages read from disk are counted in both terms. |
| `bufferpoolIndexHitPercent` | gauge | Hit percentage for index pages. |
| `bufferpoolIndexReadsLogical` | delta | Logical reads of index pages, including temporary table spaces. |
| `bufferpoolIndexReadsPhysical` | delta | Physical reads of index pages, including temporary table spaces. |
| `bufferpoolIndexReadsTotal` | delta | Logical plus physical reads of index pages. A convenience sum, not a Db2 monitor element; pages read from disk are counted in both terms. |
| `bufferpoolXdaHitPercent` | gauge | Hit percentage for XML storage object (XDA) pages. |
| `bufferpoolXdaReadsLogical` | delta | Logical reads of XDA pages, including temporary table spaces. |
| `bufferpoolXdaReadsPhysical` | delta | Physical reads of XDA pages, including temporary table spaces. |
| `bufferpoolXdaReadsTotal` | delta | Logical plus physical reads of XDA pages. A convenience sum, not a Db2 monitor element; pages read from disk are counted in both terms. |
| `bufferpoolGroupHitPercent` | gauge | Overall group bufferpool hit percentage. Published only on DB2 pureScale, and only when the pool had group bufferpool reads. |
| `bufferpoolGroupColumnHitPercent` | gauge | Group bufferpool hit percentage for column-organized pages. Published only on DB2 pureScale, and only when the pool had group bufferpool reads. |
| `bufferpoolGroupDataHitPercent` | gauge | Group bufferpool hit percentage for data pages. Published only on DB2 pureScale, and only when the pool had group bufferpool reads. |
| `bufferpoolGroupIndexHitPercent` | gauge | Group bufferpool hit percentage for index pages. Published only on DB2 pureScale, and only when the pool had group bufferpool reads. |
| `bufferpoolGroupXdaHitPercent` | gauge | Group bufferpool hit percentage for XDA pages. Published only on DB2 pureScale, and only when the pool had group bufferpool reads. |

The five `bufferpoolGroup*` metrics are published only when the group bufferpool was used, which means pureScale. On other servers a pool reports 20 of the 25 metrics.

### Db2TablespaceSample

One sample per tablespace, with the extra dimension `tablespaceName`.

| Attribute | Type | Description |
|-----------|------|-------------|
| `tablespaceName` | attribute | Tablespace name. |
| `tablespaceSize` | gauge | Total size in bytes (total pages x page size). |
| `tablespaceUsable` | gauge | Usable size in bytes: (total pages minus overhead pages) x page size. For SMS tablespaces Db2 reports usable equal to total. |
| `tablespaceUsed` | gauge | Used size in bytes (used pages x page size). For SMS tablespaces Db2 reports used equal to total. |
| `tablespaceUtilized` | gauge | Percentage of usable space that is used (used pages / usable pages x 100), the same ratio as Db2's `TBSP_UTILIZATION_PERCENT`. The integration publishes `0` when usable pages is `0` (Db2's view reports `-1` when usable pages are unavailable), and does not publish it when either page count is missing. |
| `tablespaceState` | attribute | Current state as reported by Db2, for example `NORMAL`, `QUIESCED_SHARE`, `QUIESCED_UPDATE` or `QUIESCED_EXCLUSIVE`. When more than one state applies, Db2 joins them with `+` (for example `QUIESCED_EXCLUSIVE+LOAD_PENDING`). The value is published unchanged, so alert conditions that must catch combinations should use `LIKE` instead of `= 'NORMAL'`. |

On the Db2 11.5 and 12.1 test databases, the system temporary tablespace `TEMPSPACE1` reports total, usable and used pages all equal to 1, so `tablespaceUtilized` is 100. Db2 documents that SMS tablespaces report used and usable equal to total. Treat this as expected and exclude it from alert conditions, for example with `WHERE tablespaceName != 'TEMPSPACE1'`.

### Db2CustomSample

Published for each row of each [custom query](#custom-queries) (or under the `sample_name` you choose).

| Attribute | Type | Description |
|-----------|------|-------------|
| `db2Hostname`, `port`, `database` | attribute | Identity attributes. |
| `query` | attribute | The query `name`. If you did not name it: `custom<N>`, where N is its position in the file, or `inline` for `CUSTOM_METRICS_QUERY`. |
| *column label* | gauge, delta, rate or attribute | One attribute per result column, typed as described in [Custom Queries](#custom-queries). |

### Db2TopQuerySample

Published when `COLLECT_TOP_QUERIES` is `true`. One sample per statement and member, with the extra dimensions `executableId` and `member`. One statement text can have several package cache entries (for example under a different current schema or isolation level), each with its own `executableId`, and each is its own sample. Values are cumulative since the statement's section was inserted into the package cache, and Db2 adds an execution to them when it finishes. All time metrics are in **milliseconds**.

| Attribute | Type | Description |
|-----------|------|-------------|
| `executableId` | attribute | Executable ID of the statement in the package cache. Db2 reports an opaque binary token; the integration shows it as hexadecimal. |
| `member` | attribute | Database member that holds the package cache entry. `0` on a single-member database. |
| `statementText` | attribute | Statement text, cut to 4094 characters. May contain literal values; see the [privacy note](#extras-top-sql-long-running-sql-and-lock-waits). |
| `sectionType` | attribute | Section type of the statement: `D` for a dynamic SQL statement, `S` for a static one. |
| `numExecutions` | gauge | Number of times the statement was executed. |
| `totalActTime` | gauge | Total time spent executing the statement, in milliseconds. |
| `totalActWaitTime` | gauge | Total time spent waiting while executing the statement, in milliseconds. |
| `totalCpuTime` | gauge | Total CPU time used (user plus system), in milliseconds. Db2 reports microseconds; the integration converts. |
| `lockWaitTime` | gauge | Total time spent waiting on locks, in milliseconds. |
| `rowsRead` | gauge | Total rows read. This is the number of rows Db2 had to read, not the number returned. |
| `rowsReturned` | gauge | Total rows returned. |
| `avgActTime` | gauge | `totalActTime` / `numExecutions`, in milliseconds. `numExecutions` counts every execution, including any that ran while activity metrics were off, so the average can read low then. Present only when `numExecutions` is above 0. |
| `avgCpuTime` | gauge | `totalCpuTime` / `numExecutions`, in milliseconds. The same caveat as `avgActTime` applies. Present only when `numExecutions` is above 0. |

### Db2LongRunningQuerySample

Published when `COLLECT_LONG_RUNNING_QUERIES` is `true`, for activities that started more than `LONG_RUNNING_THRESHOLD_SECONDS` ago and have not completed. One sample per activity, with the extra dimensions `applicationHandle`, `uowId`, `activityId` and `member`. One application can have several open activities at once, for example a stored procedure and the statements it runs, or a cursor held across a commit, and each has its own sample.

| Attribute | Type | Description |
|-----------|------|-------------|
| `applicationHandle` | attribute | Application handle that is running the activity. |
| `elapsedSeconds` | gauge | Wall-clock seconds since the activity started doing work, measured against the database server's clock. It includes time spent idle or waiting, not only executing time. |
| `activityId` | attribute | Activity ID within the unit of work. Part of the sample identity. |
| `uowId` | attribute | Unit of work ID. Part of the sample identity. |
| `member` | attribute | Database member of the activity. `0` on a single-member database. |
| `activityState` | attribute | Activity state, for example `EXECUTING`, `IDLE`, `INITIALIZING` or `CANCEL_PENDING`. |
| `activityType` | attribute | Activity type: `CALL`, `DDL`, `LOAD`, `OTHER`, `READ_DML` or `WRITE_DML`. |
| `applicationName` | attribute | Name of the application that submitted the activity. |
| `executableId` | attribute | Executable ID of the statement, as hexadecimal. Absent when the activity is not an SQL statement. |
| `statementText` | attribute | Statement text, cut to 4094 characters. May contain literal values. |
| `totalCpuTime` | gauge | CPU time that Db2 reports for the activity, in milliseconds (converted from microseconds). |
| `rowsRead` | gauge | Rows read by the activity, as reported by Db2. |
| `rowsReturned` | gauge | Rows returned to the application, as reported by Db2. Db2 can report `0` for an activity that is still executing. |

### Db2LockWaitSample

Published when `COLLECT_LOCK_WAITS` is `true`. One sample per request that is currently waiting for a lock, with the extra dimension `applicationHandle` (the waiting application).

| Attribute | Type | Description |
|-----------|------|-------------|
| `applicationHandle` | attribute | Application handle of the application that is waiting. |
| `holderApplicationHandle` | attribute | Application handle of the application that holds the lock. Absent if Db2 cannot find the holder. `0` means the holder no longer exists (a transaction that is being rolled back, or an in-doubt transaction). |
| `lockName` | attribute | Internal lock name. You can decode it with the `MON_FORMAT_LOCK_NAME` table function. |
| `lockObjectType` | attribute | Type of object that is locked, for example `TABLE`, `ROW`, `TABLESPACE` or `KEYVALUE`. |
| `lockMode` | attribute | Mode in which the lock is held by the holder. Absent if the holder is unknown. |
| `lockModeRequested` | attribute | Mode that the waiting application requested. |
| `executableId` | attribute | Hexadecimal executable ID of the waiting statement. |
| `waitSeconds` | gauge | Seconds the request has been waiting, measured against the database server's clock. Absent if Db2 returns no wait start time. |

### Tablespace State-Change Event

When a tablespace changes state between two runs, the integration publishes an event. It arrives in New Relic as an `InfrastructureEvent`.

| Attribute | Type | Description |
|-----------|------|-------------|
| `category` | attribute | `Db2TablespaceStateChange`. |
| `summary` | attribute | `Tablespace <name> changed state from <old> to <new>`, for example `Tablespace USERSPACE1 changed state from NORMAL to QUIESCED_SHARE`. The old and new state can each be several states joined with `+`. |
| `db2Hostname`, `port`, `database` | attribute | Identity attributes. |
| `tablespaceName` | attribute | Tablespace that changed. |
| `tablespaceState` | attribute | The new state. |
| `previousTablespaceState` | attribute | The state at the previous run. |

Notes:

- No event is produced on the first run (the states are only recorded), for a newly created tablespace, or after the stored state expired. A tablespace with an empty or missing state is ignored.
- Only the state at each run is seen. If a tablespace goes `NORMAL` to `QUIESCED_SHARE` and back between two runs, no event is produced; if it goes through several states, one event reports the change from the first to the last. Use a polling interval shorter than the shortest state you care about.
- A state that is held across two or more runs can also be detected without the event, from the `tablespaceState` attribute of `Db2TablespaceSample`.
- When the connection or the tablespace query fails, the stored states are left untouched, so no false event appears when the database returns.

### Inventory Items

Inventory is published for the `db2-instance` entity under these keys:

| Item key | Fields | Description |
|----------|--------|-------------|
| `dbm.<name>` | `value`, `deferredValue` | One item per database manager configuration parameter. `deferredValue` is present only when a pending change differs from the current value. |
| `db.<name>` | `value`, `deferredValue` | One item per database configuration parameter. The `DBCFG` view returns one row per active member; only the current member's value is published. |
| `instance` | `name`, `version`, `releaseNumber`, `buildLevel`, `ptf`, `fixpackNumber`, `members`, `partitions` | Instance and release information from `ENV_INST_INFO`: `name` is `INST_NAME`, `version` is `SERVICE_LEVEL` (the `db2level` text), `releaseNumber` is `RELEASE_NUM` (an internal release number), `buildLevel` is `BLD_LEVEL`, `ptf` is `PTF` (may be empty), `fixpackNumber` is `FIXPACK_NUM`, `members` is `NUM_MEMBERS` and `partitions` is `NUM_DBPARTITIONS`. |

Parameters whose flag is `AUTOMATIC` (Db2 manages the value) or `COMPUTED` (Db2 derives the value; database configuration only) publish that flag word instead of the current number, so self-tuning does not create inventory changes on every run. Parameters with a `NULL` value are skipped, and parameters with an empty value are published as empty. Expect roughly 250 items per database. If a view returns an error or no rows, a warning is logged and the items from that view are skipped; the other views still publish. `canConnect` and the metrics are not affected.

### Diagnostic Log Attributes

With [Mode 1](#mode-1-remote-collection-through-sql), each diagnostic record becomes a `Log` event with `logtype` set to `db2diag`. Attributes with no value are omitted.

| Attribute | Type | Description |
|-----------|------|-------------|
| `message` | string | The full text of the record (the `FULLREC` column of `PD_GET_DIAG_HIST`), multi-line. For most rows this is the `db2diag.log` record. Notification (`DX`) rows can come from the administration notification log and have a different layout. |
| `timestamp` | integer | Record time, in epoch milliseconds. |
| `logtype` | string | Always `db2diag`. |
| `entityName` | string | The entity name, `<HOSTNAME>/<DATABASE>` with the database upper-cased. |
| `reportingEndpoint` | string | `<HOSTNAME>:<PORT>`. |
| `db2Hostname` | string | The configured `HOSTNAME`, as on the samples. |
| `port` | string | The configured `PORT`. |
| `database` | string | The configured `DATABASE`, upper-cased. |
| `recordId` | string | Record ID from the record header, for example `I182507E532`. Absent on notification-format rows. |
| `level` | string | Severity word from the record header: `Info`, `Warning`, `Error`, `Severe`, `Critical` or `Event`. A source suffix such as `(OS)` is dropped. |
| `pid` | integer | Process ID. |
| `tid` | integer | Thread ID. |
| `processName` | string | Process name, for example `db2sysc 0`. |
| `instanceName` | string | DB2 instance name. |
| `member` | integer | Member number. |
| `dbPartitionNum` | integer | Database partition number (`NODE` in the record). |
| `recordHostname` | string | The DB2 server's own host name as written in the record. May differ from `db2Hostname`. |
| `dbName` | string | The database named in the record itself. Absent when the record has no database. May differ from `database`, because the diagnostic log covers the whole instance. |
| `applicationHandle` | string | Application handle, for example `0-94`. |
| `applicationId` | string | Application ID. |
| `authId` | string | Authorization ID. |
| `eduId` | integer | Engine dispatchable unit (EDU) ID. |
| `eduName` | string | EDU name. |
| `product` | string | Product that logged the record, for example `DB2 UDB`. |
| `component` | string | Component that logged the record. |
| `function` | string | Function that logged the record. |
| `probe` | integer | Probe number. |
| `rectype` | string | Record type: `DI` (internal diagnostic), `DX` (external diagnostic, including notification-log records), `EI` (internal event) or `EX` (external event). `D` and `E` are filter values for `DIAG_RECTYPE` and do not appear as a record's type. |
| `impact` | string | Customer-impact level when the record has one: `None`, `Unlikely`, `Potential`, `Immediate` or `Critical`. Db2 documents them in upper case and returns them in mixed case. |
| `diagMessage` | string | The record's message text (the `MSG` column; the `MESSAGE :` line), when present. |
| `calledProduct` | string | Product of the function that returned the error (the `CALLED :` line). Set only when the error came from a function other than the one that logged the record. |
| `calledComponent` | string | Component of the function that returned the error. `-` means an operating-system call. |
| `calledFunction` | string | Function that returned the error. |
| `osError` | integer | Operating system error number, for example `13`. The symbolic name is in `message`. |
| `errorCode` | string | First token of the record's `RETCODE` line, for example `ZRC=0x8012006D=-2146303891=SQLR_CA_BUILT`. |
| `errorStack` | string | The `RETCODE` line and its continuation lines, with whitespace collapsed. This is the return-code stack from the `RETCODE :` block, not Db2's call stack. |

The agent also adds its own attributes to log events, such as `hostname` and `filePath`.

## Usage

Once configured, the New Relic Infrastructure agent runs the integration on every interval and ingests the data. To explore it:

1. Open [New Relic One](https://one.newrelic.com/).
2. Navigate to **Query Your Data**.
3. Query the event types **Db2DatabaseSample**, **Db2BufferpoolSample** and **Db2TablespaceSample** (and **Db2CustomSample**, **Db2TopQuerySample**, **Db2LongRunningQuerySample** or **Db2LockWaitSample** if you enabled them). Tablespace state changes are in **InfrastructureEvent** with `category = 'Db2TablespaceStateChange'`, and diagnostic records are in **Log** with `logtype = 'db2diag'`.

### NRQL Examples

```sql
-- Connectivity and status per database (latest values)
SELECT latest(canConnect), latest(databaseStatus), latest(databaseStatusCode)
FROM Db2DatabaseSample
FACET db2Hostname, port, database
SINCE 15 minutes ago

-- Databases the integration could not connect to
SELECT latest(canConnect)
FROM Db2DatabaseSample
WHERE canConnect = 0
FACET database
TIMESERIES 5 minutes
SINCE 1 hour ago

-- Applications and connections
SELECT average(applicationActive), average(applicationExecuting), average(connectionActive), max(connectionMax)
FROM Db2DatabaseSample
FACET database
TIMESERIES
SINCE 1 hour ago

-- Row activity per interval (delta metrics; ignore the first sample after a start)
SELECT sum(rowReadsTotal), sum(rowModifiedTotal), sum(rowReturnedTotal)
FROM Db2DatabaseSample
FACET database
TIMESERIES 5 minutes
SINCE 1 day ago

-- Deadlocks and lock timeouts per interval, and current lock waiters
SELECT sum(lockDead), sum(lockTimeouts), max(lockWaiting)
FROM Db2DatabaseSample
FACET database
TIMESERIES 5 minutes
SINCE 1 day ago

-- Transaction log utilization (alert on this value)
SELECT latest(logUtilized)
FROM Db2DatabaseSample
FACET database
SINCE 15 minutes ago

-- Days since the last backup (-1 means no backup has ever been taken)
SELECT latest(backupLatest) / 86400 AS 'Days since backup'
FROM Db2DatabaseSample
WHERE backupLatest >= 0
FACET database
SINCE 1 hour ago

-- Bufferpool hit percent per bufferpool
SELECT average(bufferpoolHitPercent), average(bufferpoolDataHitPercent), average(bufferpoolIndexHitPercent)
FROM Db2BufferpoolSample
FACET bufferpoolName
TIMESERIES
SINCE 1 hour ago

-- Tablespace utilization, excluding the temporary tablespace that always reports 100%
SELECT latest(tablespaceUtilized)
FROM Db2TablespaceSample
WHERE tablespaceName != 'TEMPSPACE1'
FACET database, tablespaceName
SINCE 15 minutes ago

-- Tablespaces that are not in the NORMAL state
SELECT latest(tablespaceState)
FROM Db2TablespaceSample
WHERE tablespaceState != 'NORMAL'
FACET tablespaceName
SINCE 15 minutes ago

-- Recent tablespace state changes
SELECT summary, tablespaceName, previousTablespaceState, tablespaceState
FROM InfrastructureEvent
WHERE category = 'Db2TablespaceStateChange'
SINCE 1 day ago

-- Diagnostic records by level (Mode 1 only: Mode 2 records are not parsed into attributes)
SELECT count(*)
FROM Log
WHERE logtype = 'db2diag'
FACET level
SINCE 1 day ago

-- Recent Error and Severe diagnostic records (Mode 1 only)
SELECT timestamp, entityName, component, function, diagMessage
FROM Log
WHERE logtype = 'db2diag' AND level IN ('Error', 'Severe')
SINCE 1 day ago
LIMIT 50

-- Top SQL by total activity time (needs COLLECT_TOP_QUERIES)
SELECT latest(totalActTime), latest(numExecutions), latest(avgActTime), latest(statementText)
FROM Db2TopQuerySample
FACET executableId, member
SINCE 15 minutes ago
LIMIT 20

-- Long-running SQL (needs COLLECT_LONG_RUNNING_QUERIES)
SELECT max(elapsedSeconds), latest(activityState), latest(applicationName), latest(statementText)
FROM Db2LongRunningQuerySample
FACET applicationHandle, uowId, activityId, member
SINCE 1 hour ago

-- Current lock waits and who is blocking whom (needs COLLECT_LOCK_WAITS)
SELECT max(waitSeconds), latest(lockMode), latest(lockModeRequested)
FROM Db2LockWaitSample
FACET applicationHandle, holderApplicationHandle
SINCE 1 hour ago

-- Results of a custom query
SELECT latest(dataLogicalReads)
FROM Db2CustomSample
WHERE query = 'bufferpool_data_reads'
FACET bufferpoolName
SINCE 15 minutes ago
```

### Dashboard

The repository includes a template dashboard, `dashboards/db2-dashboard.json`. To import it:

1. Download or copy `dashboards/db2-dashboard.json` from the repository.
2. The JSON has `accountIds` set to an empty list, so you must update each `accountIds` block with your `accountId` (otherwise, import will fail).
3. In New Relic, go to **Dashboards**, choose **Import dashboard**, and paste or upload the JSON.

## Troubleshooting

### Common Issues

1. **No data in New Relic**
   - Check the Infrastructure agent log, `/var/log/newrelic-infra/newrelic-infra.log`, for errors from `nri-db2`.
   - Confirm that the binary is at `/var/db/newrelic-infra/custom-integrations/nri-db2` and is executable, and that `db2-config.yml` is in `/etc/newrelic-infra/integrations.d/`.
   - Run the integration by hand (see item 10) to see the exact error.
   - Restart the agent after any change: `sudo systemctl restart newrelic-infra`.
   - `failed to decode obfuscated secret` in the agent log: the `secret` in the `variables:` block is not the `obfuscatedValue` printed by `newrelic agent config obfuscate`, or the placeholder was left in. Fix the value, or comment the block out and use a plain `PASSWORD`.

2. **`SQL0551N` / SQLSTATE `42501` (not authorized)**
   - The monitoring user is missing the grants. Connect to the database as the instance owner and run `sql/grants.sql` (after replacing `MONUSER`), or add the user to `SYSMON`, `SQLADM` or `DBADM`.
   - In this case the integration connects, so `canConnect` is `1`, but no metrics arrive. Alert on missing data as well as on `canConnect`.
   - The same error for one collector only means that the matching optional grant is missing (see [Extras](#extras-top-sql-long-running-sql-and-lock-waits) and [Diagnostic Logs](#diagnostic-logs)).

3. **`error while loading shared libraries: libdb2.so.1`, `GLIBC_2.34 not found` or `libcrypt.so.1`**
   - `libdb2.so.1` not found: the IBM driver is not at `/opt/ibm/clidriver`. Check that `/opt/ibm/clidriver/lib/libdb2.so.1` exists and is readable by the agent user.
   - `GLIBC_2.34`, `GLIBCXX_3.4.29` or `XCRYPT_2.0` not found: the host is too old. The host needs glibc 2.34 or newer: RHEL, Rocky or AlmaLinux 9+, Ubuntu 22.04+, Debian 12+, or Amazon Linux 2023.
   - `libcrypt.so.1` not found: install `libxcrypt-compat` on the RHEL 9 family and Amazon Linux 2023. Also check that `libxml2` and `libpam` are installed.

4. **TLS errors: `SQL20576N` or `SQL30081N`**
   - `SQL20576N`: the server name does not match the certificate. Set `HOSTNAME` to the name in the certificate, not to an IP address or an alias.
   - `SQL30081N` with TLS enabled: the certificate file is missing or not trusted, or `ENABLE_SSL` is set against a port that is not a TLS port. Check `SSL_SERVER_CERTIFICATE`, and check that the port is the DB2 TLS port.
   - Conversely, a plaintext connection to a TLS port also fails with `SQL30081N`. Make sure `ENABLE_SSL` matches the port.
   - A TLS client pointed at a plaintext port ignores `CONNECT_TIMEOUT`: with clidriver 12.1.4, a connect can hang ~6 minutes before failing with `SQL30081N`. `RUN_TIMEOUT` (default 100 s) ends such a run, publishes `canConnect=0` and logs `run exceeded RUN_TIMEOUT`. If you see that message, check that `ENABLE_SSL` matches the port. Keep `RUN_TIMEOUT` below the agent's integration `timeout` (default 120 s), or the agent kills the run before anything is published.
   - After restarting the DB2 server, confirm that it is again listening for TLS connections on the TLS port.

5. **`canConnect` is `0`**
   - The integration could not connect, or the connection was lost. The reason is in the agent log. Typical causes:
     - `SQL30081N`: the host or port is unreachable, a firewall blocks it, or the server is down.
     - `SQL30082N`: wrong user name or password.
     - `SQL30061N`: the `DATABASE` name does not exist on that server.
     - `SQL1060N`: the user does not have `CONNECT` on the database.
     - `SQL20157N` / SQLSTATE `08004`: the database is quiesced. This is expected during maintenance; the integration does not retry it.
   - A failed connection is bounded by `CONNECT_TIMEOUT` (default 10 seconds). The integration still publishes the sample with `canConnect = 0` and exits normally.

6. **Counter metrics are 0**
   - The first sample after the integration starts, after a configuration change, or after the state expired reports `0` for all `delta` attributes. This is expected; ignore the first sample.
   - After a counter reset (a database reactivation or `db2stop` / `db2start`), the affected counters have no value for one run.
   - If the polling `interval` is 6 minutes or longer, every run is a first run. Lower the interval or raise `CACHE_TTL`.

7. **No inventory, or no extras samples**
   - Inventory needs the run to publish inventory and `inventory_source: config/db2` in the integration block. If you pass `METRICS`, `EVENTS` or `INVENTORY` settings, only those data types are published.
   - Extras samples (`Db2TopQuerySample`, `Db2LongRunningQuerySample`, `Db2LockWaitSample`) appear only when the matching flag is `true` **and** the grant is in place. Long-running SQL and lock waits also only produce samples while there is something to report: an activity over the threshold, or a lock wait in progress.
   - Check the agent log for a line like `... failed, its samples are skipped`.

8. **Diagnostic logs do not arrive**
   - Mode 1: check that `COLLECT_DIAG_LOGS` is `true`, and that you copied `db2-diag-jsonl-log.yml.example` to `/etc/newrelic-infra/logging.d/db2-diag-jsonl-log.yml` (without the `.example` suffix).
   - Check that `DIAG_LOG_DIR` exists and that files named `db2diag-*.jsonl` are being written there. The first run writes nothing (it only records the starting point).
   - Check that the user running the agent can read `DIAG_LOG_DIR` and its files, and that the `file:` path in the `logging.d` file matches `DIAG_LOG_DIR`.
   - Only records written after the first run are collected. An idle instance writes very few records.
   - Duplicate messages in the Logs UI usually mean that Mode 1 and Mode 2 are both active for the same instance. Use only one.

9. **Password contains a semicolon (`;`)**
   - The integration refuses to start with a password that contains `;`, because the connection string cannot carry it. Change the monitoring user's password. Curly braces are allowed.

10. **Running the integration by hand**
    - Run the same command the agent runs, with your settings in the environment, to see the output and any errors:
      ```bash
      HOSTNAME=db2-prod-01.example.com PORT=50000 DATABASE=SALESDB USERNAME=monuser PASSWORD='<password>' \
        /var/db/newrelic-infra/custom-integrations/nri-db2 -pretty -verbose
      ```
    - Limit the output with `-metrics`, `-inventory` or `-events`, for example `-inventory -pretty`. Without them, all three are published.
    - Use the same environment variables as in the agent's `env:` block. A different set of arguments uses a different state store, so counters start from zero.

11. **Unexpected values**
    - `tablespaceUtilized` is 100 for `TEMPSPACE1`: expected; exclude it.
    - `backupLatest` is `-1`: no backup has been taken.
    - `lockWait` moves slowly: it is a lifetime average since the database was activated.

12. **No samples at all while Db2 is slow, or `canConnect` goes to `0` although Db2 is up**
    - The integration publishes once, at the end of a run. The agent stops a run that lasts longer than the `timeout` of its `integrations:` entry (default `120s`), and a stopped run publishes nothing, not even `canConnect`. The agent logs the stop only at debug level (`HeartBeat timeout exceeded`).
    - To stay ahead of that, the integration has its own limit, `RUN_TIMEOUT` (default 100 seconds). If a run exceeds it, the integration publishes a `Db2DatabaseSample` with `canConnect = 0` and exits with code 0, so a stalled run shows up as `canConnect = 0` instead of as missing data.
    - A run can last that long when Db2 accepts the connection but statements stall: connect time plus one `QUERY_TIMEOUT` per statement. The connect time is up to twice `CONNECT_TIMEOUT`, because a failed connect is retried once. Every enabled extra, custom query, diagnostic step and the inventory add statements. With the defaults, the worst case is 20 seconds to connect (`CONNECT_TIMEOUT: 10`, retried once) plus 8 statements at `QUERY_TIMEOUT: 30` each, 260 seconds. That is more than `RUN_TIMEOUT`, so a run that stalls on every statement is cut off at 100 seconds.
    - If runs are cut off by `RUN_TIMEOUT` while Db2 is healthy but slow, pick one of these:
      - Lower `QUERY_TIMEOUT` (and `CONNECT_TIMEOUT`) so the worst case fits.
      - Raise `RUN_TIMEOUT`, and raise `timeout` in the `integrations:` entry, next to `interval`, for example `timeout: 300s`, so that `RUN_TIMEOUT` stays below it. Setting `RUN_TIMEOUT: 0` removes the integration's limit, and then only the agent `timeout` applies.
    - To confirm, run the integration by hand (item 10) and time it, or raise the agent log level to debug.

### Debug Mode

Enable verbose logging by adding to the integration config:

```yaml
integrations:
  - name: nri-db2
    # ...
    env:
      VERBOSE: "true"
```

It's recommended to only run this for 3-5 minutes.

## Building

### Prerequisites

- Docker. The build runs inside a builder image (Ubuntu 22.04 with Go and the IBM 12.1.4 client driver headers and libraries), because the integration uses cgo and links against the IBM driver.
- Network access during the image build, to download Go and the IBM driver.
- Only `linux/amd64` is built. IBM publishes no arm64 Linux client driver. On Apple Silicon, Docker runs the builder under amd64 emulation (`--platform=linux/amd64`).

### Steps

1. Clone this repo and open a terminal in the folder where you cloned it.
2. Build the builder image once:
   ```bash
   docker build --platform=linux/amd64 -t nri-db2-builder:22.04 build/builder
   ```
3. Build the binary:
   ```bash
   make linux-intel
   ```
   The binary is written to `bin/linux-amd64/nri-db2`. It has `/opt/ibm/clidriver/lib` set as its library search path. Pass `VERSION=<version>` to set the reported version.
4. Other targets, all of which run in the builder image:
   - `make test` runs the unit tests.
   - `make lint` runs `golangci-lint`.
   - `make snapshot` is a release dry run with GoReleaser. The tar.gz archive is written to `dist/`.
   - `make deps` runs `go mod download` and `go mod tidy`.
   - `make clean` removes `bin/`.
   - `make integration-test` runs the integration tests against a DB2 server. It needs a reachable DB2 test server on the Docker network `db2net`, and takes `DB2_TEST_HOSTNAME`, `DB2_TEST_PORT` and `MONUSER_PASSWORD` as variables.

## Support

<a href="https://github.com/newrelic?q=nrlabs-viz&amp;type=all&amp;language=&amp;sort="><img src="https://user-images.githubusercontent.com/1786630/214122263-7a5795f6-f4e3-4aa0-b3f5-2f27aff16098.png" height=50 /></a>

This project is actively maintained by the New Relic Labs team. Connect with us directly by [creating issues](../../issues) or [asking questions in the discussions section](../../discussions) of this repo.

We also encourage you to bring your experiences and questions to the [Explorers Hub](https://discuss.newrelic.com) where our community members collaborate on solutions and new ideas.

New Relic has open-sourced this project, which is provided AS-IS WITHOUT WARRANTY OR DEDICATED SUPPORT.

## Security

As noted in our [security policy](https://github.com/newrelic/nr-labs-pages/security/policy), New Relic is committed to the privacy and security of our customers and their data. We believe that providing coordinated disclosure by security researchers and engaging with the security community are important means to achieve our security goals.

If you believe you have found a security vulnerability in this project or any of New Relic's products or websites, we welcome and greatly appreciate you reporting it to New Relic through [HackerOne](https://hackerone.com/newrelic).

## Contributing

Contributions are welcome (and if you submit a Enhancement Request, expect to be invited to contribute it yourself :grin:). Please review our [Contributors Guide](CONTRIBUTING.md).

Keep in mind that when you submit your pull request, you'll need to sign the CLA via the click-through using CLA-Assistant. If you'd like to execute our corporate CLA, or if you have any questions, please drop us an email at opensource@newrelic.com.

## License

This project is distributed under the [Apache 2 license](LICENSE).
