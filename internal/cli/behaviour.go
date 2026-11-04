package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/behaviour"
	"github.com/spf13/cobra"
)

func (a *application) behaviourCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "behaviour", Short: "Apply and activate complete versioned behaviour manifests"}
	cmd.AddCommand(&cobra.Command{Use: "run RULE_ID", Short: "Run an active recurring rule now without changing its cadence", Args: cobra.ExactArgs(1), Annotations: map[string]string{"human": "true"}, RunE: func(c *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("rule ID must be a positive integer")
		}
		repo, ok := a.repo.(app.ManualScheduleRepository)
		if !ok {
			return errors.New("repository does not support manual schedules")
		}
		result, err := repo.RunSchedule(c.Context(), id)
		if err != nil {
			return err
		}
		return output(c, result)
	}})
	cmd.AddCommand(&cobra.Command{Use: "rules", Short: "List active rules with IDs for manual runs", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		rules, err := a.repo.Behaviour(c.Context())
		if err != nil {
			return err
		}
		return output(c, rules)
	}})
	for _, mode := range []string{"apply", "check"} {
		mode := mode
		var file string
		annotations := map[string]string{"human": "true"}
		if mode == "check" {
			annotations = map[string]string{"local": "true"}
		}
		sub := &cobra.Command{Use: mode + " [FILE]", Short: map[string]string{"apply": "Replace the complete manifest from a file or stdin (empty input clears it)", "check": "Validate a complete manifest without a database"}[mode], Args: cobra.MaximumNArgs(1), Annotations: annotations, RunE: func(c *cobra.Command, args []string) error {
			source, err := behaviourSource(c, args, file)
			if err != nil {
				return err
			}
			rules, err := behaviour.Parse(source)
			if err != nil {
				return err
			}
			if mode == "check" {
				return output(c, rules)
			}
			repo, ok := a.repo.(app.ManifestRepository)
			if !ok {
				return errors.New("repository does not support versioned manifests")
			}
			m, err := repo.ApplyManifest(c.Context(), source)
			if err != nil {
				return err
			}
			return output(c, m)
		}}
		if mode == "apply" {
			sub.Aliases = []string{"set", "create"}
		}
		sub.Flags().StringVarP(&file, "file", "f", "", "Read complete manifest from this file ('-' for stdin)")
		cmd.AddCommand(sub)
	}
	cmd.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls", "versions"}, Short: "List immutable versions including their complete original source", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		repo, ok := a.repo.(app.ManifestRepository)
		if !ok {
			return errors.New("repository does not support versioned manifests")
		}
		versions, err := repo.Manifests(c.Context())
		if err != nil {
			return err
		}
		return output(c, versions)
	}})
	cmd.AddCommand(&cobra.Command{Use: "activate ID", Short: "Activate a previously saved complete manifest", Args: cobra.ExactArgs(1), Annotations: map[string]string{"human": "true"}, RunE: func(c *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("manifest ID must be a positive integer")
		}
		repo, ok := a.repo.(app.ManifestRepository)
		if !ok {
			return errors.New("repository does not support versioned manifests")
		}
		return repo.ActivateManifest(c.Context(), id)
	}})
	return cmd
}

func behaviourSource(c *cobra.Command, args []string, file string) (string, error) {
	if len(args) > 0 && c.Flags().Changed("file") {
		return "", errors.New("provide either a FILE argument or --file, not both")
	}
	if c.Flags().Changed("file") && file == "" {
		return "", errors.New("--file requires a nonempty path (or '-' for stdin)")
	}
	if len(args) > 0 {
		file = args[0]
		if file == "" {
			return "", errors.New("FILE must not be empty")
		}
	}
	if file == "" || file == "-" {
		return input(c)
	}
	f, err := os.Open(file)
	if err != nil {
		return "", fmt.Errorf("open behaviour file %q: %w", file, err)
	}
	defer f.Close()
	source, err := readBody(f)
	if err != nil {
		return "", fmt.Errorf("read behaviour file %q: %w", file, err)
	}
	return source, nil
}
