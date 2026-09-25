package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

var (
	// ErrLeaseHeld is returned by AcquireLease when another holder owns the lease.
	ErrLeaseHeld = errors.New("lease held by another process")
	// ErrLeaseLost reports that a held lease was taken over, deleted or not renewed in time.
	ErrLeaseLost = errors.New("lease lost")
)

// MinLeaseTTL is the shortest lease TTL accepted.
const MinLeaseTTL = time.Second

// Lease is an exclusive, expiring claim on one key of a JetStream KV bucket (the bucket's TTL
// is the lease TTL). The holder renews it every TTL/3 with a compare-and-set on the revision it
// owns; a holder that crashes stops renewing and the key expires after at most TTL.
//
// Valid() is a local, conservative deadline: 2/3 TTL after the SEND time of the last successful
// renewal (the server can only expire the key TTL after the write, which is later). Holders must
// check it before every externally visible action (publish, ack), so a holder that is paused or
// cut off stops acting before a successor can acquire. It is still not a fencing token.
type Lease struct {
	kv     jetstream.KeyValue
	key    string
	holder string
	ttl    time.Duration

	mu       sync.Mutex
	rev      uint64
	deadline time.Time

	lost     chan struct{}
	lostOnce sync.Once
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

type leaseValue struct {
	Holder   string    `json:"holder"`
	Acquired time.Time `json:"acquired"`
}

// leaseBucket returns the bucket, creating it with ttl if absent. An existing bucket is never
// modified: a process configured with a different TTL is refused instead of changing the
// running holder's expiry.
func (j *JetStream) leaseBucket(ctx context.Context, bucket string, ttl time.Duration) (jetstream.KeyValue, error) {
	for attempt := 0; attempt < 2; attempt++ {
		kv, err := j.js.KeyValue(ctx, bucket)
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			kv, err = j.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
				Bucket: bucket, TTL: ttl, History: 1, Storage: jetstream.FileStorage, MaxBytes: 1 << 20,
				Description: "single-instance leases (P-02)",
			})
			if errors.Is(err, jetstream.ErrBucketExists) {
				continue // created concurrently: look it up again
			}
		}
		if err != nil {
			return nil, fmt.Errorf("lease: bucket %s: %w", bucket, err)
		}
		st, err := kv.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("lease: bucket %s status: %w", bucket, err)
		}
		if st.TTL() != ttl {
			return nil, fmt.Errorf("lease: bucket %s has TTL %s but this process is configured with %s; "+
				"use the same TTL (ENGINE_LEASE_TTL), or delete the bucket while no holder runs", bucket, st.TTL(), ttl)
		}
		return kv, nil
	}
	return nil, fmt.Errorf("lease: bucket %s: could not create or open", bucket)
}

// AcquireLease takes key for holder (holder must be unique per process), or fails with
// ErrLeaseHeld naming the current holder. The lease is renewed until Release.
func (j *JetStream) AcquireLease(ctx context.Context, bucket, key, holder string, ttl time.Duration) (*Lease, error) {
	if ttl < MinLeaseTTL {
		return nil, fmt.Errorf("lease: TTL %s below minimum %s", ttl, MinLeaseTTL)
	}
	kv, err := j.leaseBucket(ctx, bucket, ttl)
	if err != nil {
		return nil, err
	}
	val, _ := json.Marshal(leaseValue{Holder: holder, Acquired: time.Now().UTC()})
	sent := time.Now()
	rev, err := kv.Create(ctx, key, val)
	if errors.Is(err, jetstream.ErrKeyExists) {
		cur := "unknown holder"
		if e, gerr := kv.Get(ctx, key); gerr == nil {
			var v leaseValue
			if json.Unmarshal(e.Value(), &v) == nil {
				cur = fmt.Sprintf("%s (holding since %s, last renewed %s ago; expires within %s if it stops renewing)",
					v.Holder, v.Acquired.Format(time.RFC3339), time.Since(e.Created()).Round(time.Second), ttl)
			}
		}
		return nil, fmt.Errorf("%w: %s/%s is held by %s", ErrLeaseHeld, bucket, key, cur)
	}
	if err != nil {
		return nil, fmt.Errorf("lease: acquire %s/%s: %w", bucket, key, err)
	}
	l := &Lease{kv: kv, key: key, holder: holder, ttl: ttl, rev: rev, deadline: sent.Add(ttl * 2 / 3),
		lost: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	go l.heartbeat(val)
	return l, nil
}

// Lost is closed when the lease can no longer be trusted to be ours.
func (l *Lease) Lost() <-chan struct{} { return l.lost }

// Valid reports whether the holder may still act: not lost and within the local deadline.
func (l *Lease) Valid() error {
	select {
	case <-l.lost:
		return ErrLeaseLost
	default:
	}
	l.mu.Lock()
	d := l.deadline
	l.mu.Unlock()
	if time.Now().After(d) {
		return fmt.Errorf("%w: not renewed since %s", ErrLeaseLost, d.Add(-l.ttl*2/3).Format(time.RFC3339Nano))
	}
	return nil
}

func (l *Lease) markLost(reason string) {
	l.lostOnce.Do(func() {
		log.Printf("lease: %s: %s is no longer held by %s: %s", ErrLeaseLost, l.key, l.holder, reason)
		close(l.lost)
	})
}

func (l *Lease) heartbeat(val []byte) {
	defer close(l.done)
	t := time.NewTicker(l.ttl / 3)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
		}
		if lost, reason := l.renew(val); lost {
			l.markLost(reason)
			return
		}
	}
}

// renew performs one compare-and-set renewal. It reports lost when the revision moved to
// someone else, or when no renewal succeeded before the local deadline.
func (l *Lease) renew(val []byte) (lost bool, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), l.ttl/3)
	defer cancel()
	// Only this goroutine changes rev; the lock guards readers of deadline, never network calls.
	l.mu.Lock()
	cur, deadline := l.rev, l.deadline
	l.mu.Unlock()
	set := func(rev uint64, d time.Time) {
		l.mu.Lock()
		l.rev, l.deadline = rev, d
		l.mu.Unlock()
	}
	sent := time.Now()
	rev, err := l.kv.Update(ctx, l.key, val, cur)
	if err == nil {
		set(rev, sent.Add(l.ttl*2/3))
		return false, ""
	}
	if errors.Is(err, jetstream.ErrKeyExists) { // revision moved: ours (lost reply) or someone else's
		if e, gerr := l.kv.Get(ctx, l.key); gerr == nil && string(e.Value()) == string(val) {
			// An earlier renewal succeeded but its reply was lost: adopt that revision.
			set(e.Revision(), e.Created().Add(l.ttl*2/3))
			return false, ""
		}
		return true, "revision changed: " + err.Error()
	}
	if time.Now().After(deadline) {
		return true, fmt.Sprintf("not renewed in time (last error: %v)", err)
	}
	log.Printf("lease: renew %s failed (will retry): %v", l.key, err)
	return false, ""
}

// Release stops renewing and deletes the key if it is still ours, so a successor can start at
// once instead of waiting for the TTL.
func (l *Lease) Release(ctx context.Context) error {
	l.stopOnce.Do(func() { close(l.stop) })
	<-l.done
	select {
	case <-l.lost:
		return nil // not ours any more: never delete someone else's lease
	default:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.kv.Delete(ctx, l.key, jetstream.LastRevision(l.rev))
}
