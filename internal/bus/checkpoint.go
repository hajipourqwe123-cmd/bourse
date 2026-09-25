package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// StateBucket is the KV bucket for small durable service state (no TTL).
const StateBucket = "service_state"

// Checkpoint records the trading day a consumer has reached and the first stream sequence it
// applied on that day. It lets a restart know which day its ack floor belongs to even after
// the floor message itself was discarded from the stream.
type Checkpoint struct {
	Seq uint64 `json:"seq"`
	Day string `json:"day"` // Tehran trading day, YYYY-MM-DD
}

func (j *JetStream) stateBucket(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := j.js.KeyValue(ctx, StateBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		kv, err = j.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: StateBucket, History: 1,
			Storage: jetstream.FileStorage, MaxBytes: 1 << 20, Description: "service checkpoints"})
		if errors.Is(err, jetstream.ErrBucketExists) {
			kv, err = j.js.KeyValue(ctx, StateBucket)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("state: bucket %s: %w", StateBucket, err)
	}
	return kv, nil
}

// LoadCheckpoint returns the checkpoint stored under name; ok is false if there is none.
func (j *JetStream) LoadCheckpoint(ctx context.Context, name string) (cp Checkpoint, ok bool, err error) {
	kv, err := j.stateBucket(ctx)
	if err != nil {
		return cp, false, err
	}
	e, err := kv.Get(ctx, name)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return cp, false, nil
	}
	if err != nil {
		return cp, false, fmt.Errorf("state: get %s: %w", name, err)
	}
	if err := json.Unmarshal(e.Value(), &cp); err != nil {
		return cp, false, fmt.Errorf("state: decode %s: %w", name, err)
	}
	return cp, true, nil
}

// SaveCheckpoint stores cp under name.
func (j *JetStream) SaveCheckpoint(ctx context.Context, name string, cp Checkpoint) error {
	kv, err := j.stateBucket(ctx)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(cp)
	if _, err := kv.Put(ctx, name, b); err != nil {
		return fmt.Errorf("state: put %s: %w", name, err)
	}
	return nil
}
