package ducklake

import (
	"errors"
	"os"
)

// libpq environment mappings follow PostgreSQL's libpq-envars documentation.
// An explicit connection keyword takes precedence over its environment default.
// Check these during Validate as well as Open so provisioning cannot precede a
// known-unsafe ambient default. Never mutate process-wide environment.
// https://www.postgresql.org/docs/current/libpq-envars.html
var libpqEnvironment = map[string]string{
	"PGHOST": "host", "PGHOSTADDR": "hostaddr", "PGPORT": "port",
	"PGDATABASE": "dbname", "PGUSER": "user", "PGPASSWORD": "password",
	"PGPASSFILE": "passfile", "PGCHANNELBINDING": "channel_binding",
	"PGOPTIONS": "options", "PGAPPNAME": "application_name",
	"PGSSLMODE": "sslmode", "PGREQUIRESSL": "sslmode",
	"PGSSLCERT": "sslcert", "PGSSLKEY": "sslkey", "PGSSLROOTCERT": "sslrootcert",
	"PGSSLCRL": "sslcrl", "PGSSLCRLDIR": "sslcrldir", "PGSSLSNI": "sslsni",
	"PGSSLMINPROTOCOLVERSION": "ssl_min_protocol_version",
	"PGSSLMAXPROTOCOLVERSION": "ssl_max_protocol_version",
	"PGCONNECT_TIMEOUT":       "connect_timeout", "PGTARGETSESSIONATTRS": "target_session_attrs",
	"PGSSLNEGOTIATION": "sslnegotiation", "PGREQUIREAUTH": "require_auth",
	"PGSSLCOMPRESSION": "sslcompression", "PGSSLCERTMODE": "sslcertmode",
	"PGREQUIREPEER": "requirepeer", "PGGSSENCMODE": "gssencmode",
	"PGKRBSRVNAME": "krbsrvname", "PGGSSLIB": "gsslib", "PGGSSDELEGATION": "gssdelegation",
	"PGCLIENTENCODING": "client_encoding", "PGLOADBALANCEHOSTS": "load_balance_hosts",
	"PGMINPROTOCOLVERSION": "min_protocol_version", "PGMAXPROTOCOLVERSION": "max_protocol_version",
	// Services and session defaults have no accepted explicit representation.
	"PGSERVICE": "", "PGDATESTYLE": "", "PGTZ": "", "PGGEQO": "",
}

func validateAmbientConnection(raw string) error {
	fields := connectionFields(raw)
	// prepare always appends this override, even if PGPASSFILE is set.
	fields["passfile"] = true
	for environment, field := range libpqEnvironment {
		if os.Getenv(environment) != "" && (field == "" || !fields[field]) {
			return errors.New("DuckLake ambient libpq default is not overridden by an explicit nonempty resolved connection setting: " + environment)
		}
	}
	// PGSERVICEFILE/PGSYSCONFDIR cannot select a service on their own. Since
	// service= and ambient PGSERVICE are rejected, neither can affect a connection.
	// PGLOCALEDIR affects diagnostics only, which are suppressed. Unknown PG*
	// application variables are not libpq defaults and are deliberately ignored.
	return nil
}
