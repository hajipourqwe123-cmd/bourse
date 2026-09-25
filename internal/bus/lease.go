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

// Lease is an exclusive, expiring claim on one key of a JetStream KV bucket (the bucket's TTL
// is the lease TTL). The holder renews it every TTL/3 with a compare-and-set on the revision it
// owns; a holder that crashes stops renewing and the key expires after at most TTL.
//
// It prevents a second process from starting while one is alive. It is not a fencing token:
// a holder cut off from NATS keeps running until it notices (at most TTL), so it must stop on
// Lost() (the engine does).
type Lease struct {
	kv     jetstream.KeyValue
	key    string
	holder string
	ttl    time.Duration

	mu   sync.Mutex
	rev  uint64
	lost chan struct{}
	once sync.Once
	stop chan struct{}
	done chan struct{}
}

type leaseValue struct {
	Holder   string    `json:"holder"`
	Acquired time.Time `json:"acquired"`
}

// AcquireLease creates or updates the KV bucket and takes key for holder, or fails with
// ErrLeaseHeld naming the current holder. The lease is renewed until Release.
func (j *JetStream) AcquireLease(ctx context.Context, bucket, key, holder string, ttl time.Duration) (*Lease, error) {
	kv, err := j.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: bucket, TTL: ttl, History: 1, Storage: jetstream.FileStorage, MaxBytes: 1 << 20,
		Description: "single-instance leases (P-02)",
	})
	if err != nil {
		return nil, fmt.Errorf("lease: bucket %s: %w", bucket, err)
	}
	val, _ := json.Marshal(leaseValue{Holder: holder, Acquired: time.Now().UTC()})
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
	l := &Lease{kv: kv, key: key, holder: holder, ttl: ttl, rev: rev,
		lost: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	go l.heartbeat(val)
	return l, nil
}

// Lost is closed when the lease can no longer be trusted to be ours.
func (l *Lease) Lost() <-chan struct{} { return l.lost }

func (l *Lease) markLost(reason string) {
	l.once.Do(func() {
		log.Printf("lease: %s: %s is no longer held by %s: %s", ErrLeaseLost, l.key, l.holder, reason)
		close(l.lost)
	})
}

func (l *Lease) heartbeat(val []byte) {
	defer close(l.done)
	t := time.NewTicker(l.ttl / 3)
	defer t.Stop()
	renewed := time.Now()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), l.ttl/3)
		l.mu.Lock()
		rev, err := l.kv.Update(ctx, l.key, val, l.rev)
		if err == nil {
			l.rev, renewed = rev, time.Now()
		}
		l.mu.Unlock()
		cancel()
		switch {
		case err == nil:
		case errors.Is(err, jetstream.ErrKeyExists): // revision moved: deleted, expired or taken
			l.markLost("revision changed: " + err.Error())
			return
		case time.Since(renewed) >= l.ttl:
			l.markLost(fmt.Sprintf("not renewed for %s (last error: %v)", time.Since(renewed).Round(time.Millisecond), err))
			return
		default:
			log.Printf("lease: renew %s failed (will retry): %v", l.key, err)
		}
	}
}

// Release stops renewing and deletes the key if it is still ours, so a successor can start at
// once instead of waiting for the TTL.
func (l *Lease) Release(ctx context.Context) error {
	select {
	case <-l.stop:
	default:
		close(l.stop)
	}
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
