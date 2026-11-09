package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/api-mittens/looster/internal/ducklake"
	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/objectstore"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/spf13/cobra"
)

type storageActions struct {
	configure  func(context.Context, string, string, string, string) (string, error)
	connection func(string, string) (string, error)
	validate   func(ducklake.Config) error
	attach     func(context.Context, ducklake.Config) error
}

func (a *application) configureCommand() *cobra.Command {
	return a.configureCommandWith(storageActions{
		configure:  postgres.ConfigureDatabasesNamespace,
		connection: postgres.DatabaseConnectionString,
		validate:   ducklake.Validate,
		attach:     func(ctx context.Context, cfg ducklake.Config) error { _, err := ducklake.Open(ctx, cfg); return err },
	})
}

func (a *application) configureCommandWith(actions storageActions) *cobra.Command {
	root := &cobra.Command{Use: "configure", Short: "Configure optional infrastructure (human only)"}
	var database, catalog, dataPath, binary string
	var enabled bool
	storage := &cobra.Command{
		Use: "storage", Short: "Create missing databases and migrate storage, optionally initialize DuckLake",
		Args: cobra.NoArgs, Annotations: map[string]string{"local": "true", "human": "true"},
		RunE: func(c *cobra.Command, _ []string) error {
			ns := a.v.GetString("namespace")
			catalogName := ""
			var lake ducklake.Config
			if !enabled {
				for _, flag := range []string{"ducklake-database", "ducklake-data-path", "duckdb-binary"} {
					if c.Flags().Changed(flag) {
						return fmt.Errorf("--%s requires explicit --ducklake opt-in", flag)
					}
				}
			} else {
				if ns != namespace.Default {
					return errors.New("DuckLake currently supports only the default namespace; no storage changes made")
				}
				if catalog == "" {
					return errors.New("--ducklake-database must not be empty")
				}
				catalogName = catalog
				connection, err := actions.connection(a.v.GetString("database_url"), catalog)
				if err != nil {
					return err
				}
				lake = ducklake.Config{
					Binary: binary, CatalogConnection: connection, Namespace: ns, DataPath: dataPath,
					S3: objectstore.Config{
						Endpoint: a.v.GetString("s3.endpoint"), Region: a.v.GetString("s3.region"), Bucket: a.v.GetString("s3.bucket"),
						AccessKey: a.v.GetString("s3.access_key"), SecretKey: a.v.GetString("s3.secret_key"), SessionToken: a.v.GetString("s3.session_token"),
					},
				}
				if err := actions.validate(lake); err != nil {
					return err
				}
			}
			if _, err := actions.configure(c.Context(), a.v.GetString("database_url"), database, catalogName, ns); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "Application database configured and namespace migrated. Existing databases and data retained.")
			if database != "" {
				fmt.Fprintln(c.OutOrStdout(), "The --database override is not persisted. Set LOOSTER_DATABASE_URL or PGDATABASE consistently for subsequent commands.")
			}
			if enabled {
				if err := actions.attach(c.Context(), lake); err != nil {
					return fmt.Errorf("application migrations completed and databases retained; DuckLake initialization failed, correct settings and retry: %w", err)
				}
				fmt.Fprintln(c.OutOrStdout(), "DuckLake catalog initialized. S3 write/reopen readiness requires the documented disposable integration probe. No session or event data moved.")
			}
			return nil
		},
	}
	storage.Flags().StringVar(&database, "database", "", "Application database override (default: currently configured database, or looster for fresh installs)")
	storage.Flags().BoolVar(&enabled, "ducklake", false, "Explicitly opt in to a separate DuckLake catalog and S3 data path")
	storage.Flags().StringVar(&catalog, "ducklake-database", "ducklake", "Separate PostgreSQL catalog database (requires --ducklake)")
	storage.Flags().StringVar(&dataPath, "ducklake-data-path", "", "Dedicated s3://bucket/prefix/ for DuckLake data (required with --ducklake)")
	storage.Flags().StringVar(&binary, "duckdb-binary", "duckdb", "Installed pinned DuckDB CLI executable (requires --ducklake)")
	root.AddCommand(storage)
	return root
}
