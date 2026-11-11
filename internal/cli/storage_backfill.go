package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// Storage maintenance never drops PostgreSQL objects or deletes lake files.
// The normal runtime startup establishes the durable old-writer fence first.
func (a *application) storageMaintenanceCommand() *cobra.Command {
	root := &cobra.Command{Use: "storage", Short: "Backfill and reconcile lake payload storage (human only)", Annotations: map[string]string{"human": "true"}}
	var limit int
	var confirm bool
	backfill := &cobra.Command{Use: "backfill", Short: "Replace a bounded historical payload batch with verified lake references", Args: cobra.NoArgs, Annotations: map[string]string{"human": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		if !confirm {
			return errors.New("backfill requires --confirm-readers-upgraded after upgrading all readers and stopping old binaries")
		}
		if limit < 1 {
			return errors.New("backfill --limit must be positive")
		}
		report, err := a.store.BackfillPayloads(c.Context(), limit)
		if err != nil {
			return err
		}
		return output(c, report)
	}}
	backfill.Flags().IntVar(&limit, "limit", 1000, "Maximum historical payload fields to migrate in this invocation")
	backfill.Flags().BoolVar(&confirm, "confirm-readers-upgraded", false, "Confirm all readers understand lake references and old binaries have been stopped")
	var verifyLimit int
	verify := &cobra.Command{Use: "verify", Short: "Reread remote payloads and report remaining legacy fields without deleting data", Args: cobra.NoArgs, Annotations: map[string]string{"human": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		if verifyLimit < 0 {
			return errors.New("verify --limit must not be negative")
		}
		report, err := a.store.VerifyPayloads(c.Context(), verifyLimit)
		if err != nil {
			return err
		}
		return output(c, report)
	}}
	verify.Flags().IntVar(&verifyLimit, "limit", 0, "Maximum payload fields to check (0 checks all fields)")
	root.AddCommand(backfill, verify)
	return root
}
