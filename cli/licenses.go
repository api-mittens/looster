package cli

import (
	"fmt"

	"github.com/dominikbb/looster"
	"github.com/spf13/cobra"
)

func licensesCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "licenses",
		Short:       "Print the MIT license, dependency notices and source availability",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"offline": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), looster.License, "\n", looster.ThirdPartyNotices)
			return err
		},
	}
}
