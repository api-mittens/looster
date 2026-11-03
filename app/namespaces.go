package app

import "context"

// NamespaceProvider supplies isolated repositories for dashboard requests.
// Implementations retain ownership of repository connection lifetimes.
type NamespaceProvider interface {
	DefaultNamespace() string
	Namespaces(context.Context) ([]string, error)
	Repository(context.Context, string) (Repository, error)
}
