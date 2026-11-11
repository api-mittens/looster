package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

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
		attach: func(ctx context.Context, cfg ducklake.Config) error {
			lake, err := ducklake.Open(ctx, cfg)
			if err != nil {
				return err
			}
			return lake.Close()
		},
	})
}

func (a *application) configureCommandWith(actions storageActions) *cobra.Command {
	root := &cobra.Command{Use: "configure", Short: "Configure infrastructure (human only)"}
	var database, catalog, dataPath string
	var enabled bool
	storage := &cobra.Command{
		Use: "storage", Short: "Create missing application database and migrate storage, optionally prewarm automatic DuckLake",
		Example: "  # Default: s3://<configured s3.bucket>/ducklake/\n" +
			"  looster configure storage --ducklake\n" +
			"  # Explicit dedicated prefix override:\n" +
			"  looster configure storage --ducklake --ducklake-data-path s3://my-bucket/custom-prefix/",
		Args: cobra.NoArgs, Annotations: map[string]string{"local": "true", "human": "true"},
		RunE: func(c *cobra.Command, _ []string) error {
			ns := a.v.GetString("namespace")
			if err := namespace.Validate(ns); err != nil {
				return err
			}
			catalogName := ""
			var lake ducklake.Config
			if !enabled {
				for _, flag := range []string{"ducklake-database", "ducklake-data-path"} {
					if c.Flags().Changed(flag) {
						return fmt.Errorf("--%s requires explicit --ducklake opt-in", flag)
					}
				}
			} else {
				if c.Flags().Changed("ducklake-database") && catalog == "" {
					return errors.New("--ducklake-database must not be empty")
				}
				resolvedPath := dataPath
				if !c.Flags().Changed("ducklake-data-path") && a.v.GetString("ducklake.data_path") != "" {
					resolvedPath = a.v.GetString("ducklake.data_path")
				}
				if !c.Flags().Changed("ducklake-data-path") && resolvedPath == "" {
					bucket := a.v.GetString("s3.bucket")
					if !validDuckLakeBucket(bucket) {
						return errors.New("DuckLake default data path requires a valid configured s3.bucket; no storage changes made")
					}
					resolvedPath = "s3://" + bucket + "/ducklake/"
				} else if resolvedPath == "" {
					return errors.New("--ducklake-data-path must not be empty")
				}
				resolvedPath = strings.TrimRight(resolvedPath, "/") + "/namespaces/" + ns + "/"
				catalogName = catalog
				selectedCatalog := catalog
				if selectedCatalog == "" {
					selectedCatalog = a.v.GetString("ducklake.database")
				}
				if selectedCatalog == "" {
					selectedCatalog = database
				}
				connection, err := actions.connection(a.v.GetString("database_url"), selectedCatalog)
				if err != nil {
					return err
				}
				lake = ducklake.Config{
					CatalogConnection: connection, Namespace: ns, DataPath: resolvedPath,
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
	storage.Flags().BoolVar(&enabled, "ducklake", false, "Prewarm automatic DuckLake bootstrap (normal application commands always initialize it)")
	storage.Flags().StringVar(&catalog, "ducklake-database", "", "Legacy separate catalog database override (default: configured application database; requires --ducklake)")
	storage.Flags().StringVar(&dataPath, "ducklake-data-path", "", "Dedicated S3 data path (default: s3://<configured s3.bucket>/ducklake/; requires --ducklake)")
	root.AddCommand(storage)
	return root
}

var duckLakeBucketLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// Check bucket syntax before interpolating configuration into an S3 URL. Do not
// trim or otherwise rewrite it, which could silently select a different bucket.
func validDuckLakeBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 || net.ParseIP(bucket) != nil {
		return false
	}
	for _, label := range strings.Split(bucket, ".") {
		if !duckLakeBucketLabel.MatchString(label) {
			return false
		}
	}
	return true
}
