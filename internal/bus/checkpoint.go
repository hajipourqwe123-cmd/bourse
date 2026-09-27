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
	Seq   uint64 `json:"seq"`
	Day   string `json:"day"`   // Tehran trading day, YYYY-MM-DD
	Epoch int64  `json:"epoch"` // creation time (unix ns) of the stream Seq refers to; a mismatch means it was recreated
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

// StateGet returns the value stored under key in StateBucket; ok is false if there is none.
func (j *JetStream) StateGet(ctx context.Context, key string) (val []byte, ok bool, err error) {
	kv, err := j.stateBucket(ctx)
	if err != nil {
		return nil, false, err
	}
	e, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("state: get %s: %w", key, err)
	}
	return e.Value(), true, nil
}

// StatePut stores val under key in StateBucket.
func (j *JetStream) StatePut(ctx context.Context, key string, val []byte) error {
	kv, err := j.stateBucket(ctx)
	if err != nil {
		return err
	}
	if _, err := kv.Put(ctx, key, val); err != nil {
		return fmt.Errorf("state: put %s: %w", key, err)
	}
	return nil
}

// LoadCheckpoint returns the checkpoint stored under name; ok is false if there is none.
func (j *JetStream) LoadCheckpoint(ctx context.Context, name string) (cp Checkpoint, ok bool, err error) {
	b, ok, err := j.StateGet(ctx, name)
	if err != nil || !ok {
		return cp, false, err
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		return cp, false, fmt.Errorf("state: decode %s: %w", name, err)
	}
	return cp, true, nil
}

// SaveCheckpoint stores cp under name.
func (j *JetStream) SaveCheckpoint(ctx context.Context, name string, cp Checkpoint) error {
	b, _ := json.Marshal(cp)
	return j.StatePut(ctx, name, b)
}

// RefBucket holds the whole-market previous-day totals of the carryover rules (engine and
// gateway, two rotated records each: DL-01, PR #3). A bucket of its own: service_state (1 MiB)
// is too small for four market-wide records. RefBucketBytes is checked against a measured
// record size in TestRefBucketSizing.
const (
	RefBucket      = "carryover_ref"
	RefBucketBytes = 16 << 20
)

func (j *JetStream) refBucket(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := j.js.KeyValue(ctx, RefBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		kv, err = j.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: RefBucket, History: 1,
			Storage: jetstream.FileStorage, MaxBytes: RefBucketBytes, Description: "previous-day totals (carryover rules)"})
		if errors.Is(err, jetstream.ErrBucketExists) {
			kv, err = j.js.KeyValue(ctx, RefBucket)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ref: bucket %s: %w", RefBucket, err)
	}
	return kv, nil
}

// RefGet returns the value stored under key in RefBucket; ok is false if there is none.
func (j *JetStream) RefGet(ctx context.Context, key string) (val []byte, ok bool, err error) {
	kv, err := j.refBucket(ctx)
	if err != nil {
		return nil, false, err
	}
	e, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("ref: get %s: %w", key, err)
	}
	return e.Value(), true, nil
}

// RefPut stores val under key in RefBucket.
func (j *JetStream) RefPut(ctx context.Context, key string, val []byte) error {
	kv, err := j.refBucket(ctx)
	if err != nil {
		return err
	}
	if _, err := kv.Put(ctx, key, val); err != nil {
		return fmt.Errorf("ref: put %s: %w", key, err)
	}
	return nil
}
