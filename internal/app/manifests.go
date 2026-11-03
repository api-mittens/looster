package app

import (
	"context"
	"github.com/api-mittens/looster/internal/behaviour"
)

// ManifestRepository is an optional versioned configuration port.
type ManifestRepository interface {
	ApplyManifest(ctx context.Context, source string) (behaviour.Manifest, error)
	Manifests(ctx context.Context) ([]behaviour.Manifest, error)
	ActivateManifest(ctx context.Context, id int64) error
}
