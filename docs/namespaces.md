# Namespaces

Namespaces keep independent tasks, events, tags, agents, manifests, schedules,
workers and session leases in one PostgreSQL database.

```sh
looster namespace create research
looster namespace list
looster -N research behaviour apply < behaviour.txt
looster -N research task list
looster -N research serve
looster -N research web
```

Select the namespace with global `--namespace` / `-N`, `LOOSTER_NAMESPACE`, or
`namespace = "research"` in the configuration file. Explicit flags override the
environment, which overrides the configuration file. The default is `default`.
Workers pass their selected namespace to harness subprocesses.

Names use 1-52 lowercase ASCII letters, digits, underscores or dashes, beginning
with a letter. The `default` namespace maps to the existing `public` schema, so
upgrades preserve all existing application data and migration history. Other
names map to quoted `looster_ns_<name>` schemas.

Namespace creation is explicit. `namespace create NAME` is idempotent and runs
all embedded migrations for that namespace. `db migrate` upgrades the currently
selected existing namespace. Run it for each existing namespace when upgrading:

```sh
looster db migrate
looster -N research db migrate
```

Each schema has its own Goose migration history. Database function search paths
are pinned to their schema, and application pools never fall back to `public`.
The dashboard's namespace selector routes API reads and writes to the selected
repository. Opening a missing namespace fails rather than creating it implicitly.

Namespaces are logical application isolation, not an authorization boundary for
someone with arbitrary SQL access using the same database role. Use PostgreSQL
roles and schema grants for database-level access restrictions. Namespace
creation and migrations require the appropriate schema-owner privileges.

## Integration tests

`go test ./namespace ./postgres ./cli` runs validation and configuration tests.
Set `LOOSTER_TEST_DATABASE_URL` to an accessible PostgreSQL database to include
`TestNamespaceIsolation`. That test creates two unique temporary application
schemas, verifies dispatch, events, schedules, worker visibility, function paths
and simultaneous same-identity leases, then drops only those test schemas. It
does not migrate `public` or change existing application rows.
