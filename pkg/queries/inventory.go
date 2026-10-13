package queries

// Names identify the inventory queries in log lines.
const (
	DBMCfgName   = "SYSIBMADM.DBMCFG"
	DBCfgName    = "SYSIBMADM.DBCFG"
	InstInfoName = "SYSIBMADM.ENV_INST_INFO"
)

// The inventory queries read administrative views that PUBLIC can select from, so they need no
// grant. DBCFG is filtered to the current member so a pureScale database yields one row per name.
const (
	DBMCfg = Tag + "SELECT NAME, VALUE, VALUE_FLAGS, DEFERRED_VALUE, DEFERRED_VALUE_FLAGS FROM SYSIBMADM.DBMCFG"

	DBCfg = Tag + "SELECT NAME, VALUE, VALUE_FLAGS, DEFERRED_VALUE, DEFERRED_VALUE_FLAGS " +
		"FROM SYSIBMADM.DBCFG WHERE MEMBER = CURRENT MEMBER"

	InstInfo = Tag + "SELECT INST_NAME, SERVICE_LEVEL, RELEASE_NUM, BLD_LEVEL, PTF, FIXPACK_NUM, " +
		"NUM_MEMBERS, NUM_DBPARTITIONS FROM SYSIBMADM.ENV_INST_INFO"
)
