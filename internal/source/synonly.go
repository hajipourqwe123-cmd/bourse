package source

import (
	"context"
	"errors"
	"fmt"

	"bourse/internal/model"
)

// SyntheticOnly passes through a source whose every snapshot is synthetic (model.IsSynthetic)
// and fails on the first one that is not. DEMO_CLOCK wraps its replay in it: re-timing real
// market data to a fake "now" is not allowed even locally (rules 2 and 5).
type SyntheticOnly struct{ Source }

// ErrNotSynthetic is returned (wrapped) for real data; the collector stops on it.
var ErrNotSynthetic = errors.New("not synthetic data: refused under DEMO_CLOCK")

func (s SyntheticOnly) Name() string { return "synthetic-only:" + s.Source.Name() }

func (s SyntheticOnly) Fetch(ctx context.Context) ([]model.Snapshot, error) {
	batch, err := s.Source.Fetch(ctx)
	for i := range batch {
		if !model.IsSynthetic(&batch[i]) {
			return nil, fmt.Errorf("%s (source %q): %w", batch[i].InsCode, batch[i].Source, ErrNotSynthetic)
		}
	}
	return batch, err
}
