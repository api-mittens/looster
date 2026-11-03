package cli

import (
	"github.com/spf13/cobra"

	"github.com/api-mittens/looster/internal/skills"
)

func skillsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "skills", Short: "Generate agent skills for using Looster"}
	var force bool
	generate := &cobra.Command{
		Use:         "generate [DIR]",
		Short:       "Write looster/SKILL.md under DIR (default ~/.agents/skills)",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"offline": "true"},
		RunE: func(c *cobra.Command, args []string) error {
			var dir string
			if len(args) > 0 {
				dir = args[0]
			}
			path, err := skills.Generate(dir, force)
			if err != nil {
				return err
			}
			return output(c, map[string]string{"skill": "looster", "path": path})
		},
	}
	generate.Flags().BoolVar(&force, "force", false, "Replace an existing Looster skill, including local edits")
	cmd.AddCommand(generate)
	return cmd
}
