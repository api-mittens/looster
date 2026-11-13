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
	"github.com/api-mittens/looster/internal/telemetry"
	"github.com/api-mittens/looster/internal/web"
	"github.com/spf13/cobra"
)

func (a *application) webCommand() *cobra.Command {
	var address string
	cmd := &cobra.Command{Use: "web", Short: "Serve the task dashboard", Args: cobra.NoArgs, Annotations: map[string]string{"human": "true"}, RunE: func(c *cobra.Command, _ []string) (retErr error) {
		logger, shutdown, err := telemetry.Setup(c.Context(), "looster-web", Version, c.ErrOrStderr())
		if err != nil {
			return err
		}
		defer func() {
			if shutdown(context.Background()) != nil {
				fmt.Fprintln(c.ErrOrStderr(), "Telemetry shutdown failed")
			}
		}()
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		provider := &webNamespaces{openPayloads: a.openPayloadStore, url: a.v.GetString("database_url"), selected: a.v.GetString("namespace"), stores: map[string]*postgres.Store{}, repos: map[string]app.Repository{a.v.GetString("namespace"): a.repo}}
		defer func() { retErr = errors.Join(retErr, provider.Close()) }()
		var objects app.Objects
		if a.v.GetString("s3.bucket") != "" {
			objects, err = a.objects(c.Context())
			if err != nil {
				_ = listener.Close()
				return fmt.Errorf("dashboard attachment storage: %w", err)
			}
		}
		server := &http.Server{Handler: web.WithLogger(web.HandlerWithStorage(a.repo, objects, web.WithNamespaces(provider)), logger), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
		stopped := make(chan struct{})
		shutdownDone := make(chan struct{})
		defer func() { close(stopped); <-shutdownDone }()
		go func() {
			defer close(shutdownDone)
			select {
			case <-c.Context().Done():
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if server.Shutdown(ctx) != nil {
					_ = server.Close()
				}
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
