package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/web"
	"github.com/spf13/cobra"
)

func (a *application) webCommand() *cobra.Command {
	var address string
	cmd := &cobra.Command{Use: "web", Short: "Serve the task dashboard", Args: cobra.NoArgs, Annotations: map[string]string{"human": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		provider := &webNamespaces{url: a.v.GetString("database_url"), selected: a.v.GetString("namespace"), stores: map[string]*postgres.Store{}, repos: map[string]app.Repository{a.v.GetString("namespace"): a.repo}}
		defer provider.Close()
		var objects app.Objects
		if a.v.GetString("s3.bucket") != "" {
			objects, err = a.objects(c.Context())
			if err != nil {
				_ = listener.Close()
				return fmt.Errorf("dashboard attachment storage: %w", err)
			}
		}
		server := &http.Server{Handler: web.HandlerWithStorage(a.repo, objects, web.WithNamespaces(provider)), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
		stopped := make(chan struct{})
		defer close(stopped)
		go func() {
			select {
			case <-c.Context().Done():
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = server.Shutdown(ctx)
			case <-stopped:
			}
		}()
		fmt.Fprintf(c.OutOrStdout(), "Dashboard: http://%s\n", listener.Addr())
		err = server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}}
	cmd.Flags().StringVar(&address, "listen", "127.0.0.1:8080", "Dashboard listen address")
	return cmd
}
