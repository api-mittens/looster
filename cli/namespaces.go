package cli

import (
	"context"
	"sync"

	"github.com/dominikbb/looster/app"
	"github.com/dominikbb/looster/postgres"
	"github.com/spf13/cobra"
)

func (a *application) namespaceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "namespace", Short: "Manage isolated task namespaces"}
	cmd.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Args: cobra.NoArgs, Annotations: map[string]string{"local": "true", "human": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		names, err := postgres.ListNamespaces(c.Context(), a.v.GetString("database_url"))
		if err != nil {
			return err
		}
		return output(c, names)
	}})
	cmd.AddCommand(&cobra.Command{Use: "create NAME", Short: "Ensure a namespace exists and apply its migrations", Args: cobra.ExactArgs(1), Annotations: map[string]string{"local": "true", "human": "true"}, RunE: func(c *cobra.Command, args []string) error {
		if err := postgres.EnsureNamespace(c.Context(), a.v.GetString("database_url"), args[0]); err != nil {
			return err
		}
		return output(c, map[string]string{"namespace": args[0]})
	}})
	return cmd
}

// webNamespaces owns the pools opened for namespace switches for one server.
type webNamespaces struct {
	url, selected string
	mu            sync.Mutex
	stores        map[string]*postgres.Store
	repos         map[string]app.Repository
}

var _ app.NamespaceProvider = (*webNamespaces)(nil)

func (p *webNamespaces) DefaultNamespace() string { return p.selected }
func (p *webNamespaces) Namespaces(ctx context.Context) ([]string, error) {
	return postgres.ListNamespaces(ctx, p.url)
}
func (p *webNamespaces) Repository(ctx context.Context, name string) (app.Repository, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if repo, ok := p.repos[name]; ok {
		return repo, nil
	}
	store, err := postgres.OpenNamespace(ctx, p.url, name)
	if err != nil {
		return nil, err
	}
	repo, err := app.WithPermissions(store, app.Actor{Role: app.Human})
	if err != nil {
		store.Close()
		return nil, err
	}
	p.stores[name], p.repos[name] = store, repo
	return repo, nil
}
func (p *webNamespaces) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, store := range p.stores {
		store.Close()
	}
}
