-- Monitoring grants for nri-db2.
-- Replace MONUSER with your monitoring user (a Db2 user is an operating-system user on the Db2 host).
-- Run as the instance owner while connected to the database:
--   db2 connect to <db> && db2 -tvf grants.sql
-- Without the five mandatory grants below every MON_GET_* call fails with SQL0551N / SQLSTATE 42501.
-- Membership of SYSMON, SQLADM or DBADM is an alternative to these grants.
GRANT CONNECT ON DATABASE TO USER MONUSER;
GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_INSTANCE TO USER MONUSER;
GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_DATABASE TO USER MONUSER;
GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_BUFFERPOOL TO USER MONUSER;
GRANT EXECUTE ON SPECIFIC FUNCTION SYSPROC.MON_GET_TABLESPACE TO USER MONUSER;
GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_TRANSACTION_LOG(INTEGER) TO USER MONUSER;

-- Diagnostic log collection (SYSPROC.PD_GET_DIAG_HIST). Not needed by default: on Db2 11.5.9 and
-- 12.1.x, PUBLIC holds EXECUTE WITH GRANT OPTION (SYSCAT.ROUTINEAUTH EXECUTEAUTH='G').
-- If PUBLIC has been revoked, the monitoring user gets SQL0551N / SQLSTATE 42501, and this grant
-- alone fixes it. Uncomment for hardened databases:
-- GRANT EXECUTE ON FUNCTION SYSPROC.PD_GET_DIAG_HIST TO USER MONUSER;

-- Optional extras. Each group is needed only when the matching flag is true; nobody (not even
-- PUBLIC) holds EXECUTE on these functions by default, so without the grant the monitoring user
-- gets SQL0551N / SQLSTATE 42501. Each function has one signature, so no SPECIFIC form is needed.

-- Needed only when COLLECT_TOP_QUERIES is true; nobody (not even PUBLIC) holds EXECUTE on this by default.
-- GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_PKG_CACHE_STMT TO USER MONUSER;

-- Needed only when COLLECT_LONG_RUNNING_QUERIES is true; nobody (not even PUBLIC) holds EXECUTE on this by default.
-- GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_ACTIVITY TO USER MONUSER;

-- Needed only when COLLECT_LOCK_WAITS is true; nobody (not even PUBLIC) holds EXECUTE on this by default.
-- GRANT EXECUTE ON FUNCTION SYSPROC.MON_GET_APPL_LOCKWAIT TO USER MONUSER;
