package postgres

import (
	"errors"
	"testing"

	"github.com/dominikbb/looster/internal/app"
	"github.com/jackc/pgx/v5"
)

func TestRepositoryErrorsPreserveDriverCompatibility(t *testing.T) {
	if got := repositoryError(nil); got != nil {
		t.Fatalf("nil became %v", got)
	}
	cause := errors.New("connection unavailable")
	if got := repositoryError(cause); got != cause {
		t.Fatal("nonabsence error changed")
	}
	missing := repositoryError(pgx.ErrNoRows)
	if !errors.Is(missing, app.ErrNotFound) || !errors.Is(missing, pgx.ErrNoRows) {
		t.Fatal("missing record error lost application or driver identity")
	}
}

func TestTagConfigurationCannotBypassManifest(t *testing.T) {
	store := &Store{}
	if err := store.CreateTag(t.Context(), "src/linear", "context"); err == nil {
		t.Fatal("tag creation bypassed manifest")
	}
	if err := store.RemoveTag(t.Context(), "src/linear"); err == nil {
		t.Fatal("tag removal bypassed manifest")
	}
}
