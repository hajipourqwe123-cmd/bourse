// Package source contains adapters that turn a provider's payload into canonical snapshots.
// Every adapter must: (1) never invent a value — absent fields go to Snapshot.Missing;
// (2) set IngestTime; (3) set SourceTimeEstimated when the provider gives no timestamp.
package source

import (
	"context"

	"bourse/internal/model"
)

// Source yields batches of snapshots. Fetch returns io.EOF-like ErrDone when a finite source ends.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]model.Snapshot, error)
}
