package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const leaseTTL = time.Second

func acquire(t *testing.T, j *JetStream, holder string) (*Lease, error) {
	t.Helper()
	return j.AcquireLease(ctxT(t), "test_lease", "engine", holder, leaseTTL)
}

func mustAcquire(t *testing.T, j *JetStream, holder string) *Lease {
	t.Helper()
	l, err := acquire(t, j, holder)
	if err != nil {
		t.Fatalf("%s: %v", holder, err)
	}
	return l
}

func wantHeld(t *testing.T, j *JetStream, holder, by string) {
	t.Helper()
	_, err := acquire(t, j, holder)
	if !errors.Is(err, ErrLeaseHeld) || !strings.Contains(err.Error(), by) {
		t.Fatalf("%s: want ErrLeaseHeld naming %s, got %v", holder, by, err)
	}
}

func isLost(l *Lease) bool {
	select {
	case <-l.Lost():
		return true
	default:
		return false
	}
}

func TestLeaseExclusiveAndReleased(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	wantHeld(t, j, "engine-b", "engine-a")
	if err := a.Release(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	b := mustAcquire(t, j, "engine-b") // at once, no TTL wait after a clean release
	defer b.Release(ctxT(t))
	wantHeld(t, j, "engine-c", "engine-b")
}

func TestLeaseHeartbeatOutlivesTTL(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	defer a.Release(ctxT(t))
	time.Sleep(3 * leaseTTL)
	if isLost(a) {
		t.Fatal("renewed lease reported lost")
	}
	wantHeld(t, j, "engine-b", "engine-a")
}

// A crashed holder stops renewing: the key expires within the TTL and a successor can start.
func TestLeaseExpiresAfterCrash(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	close(a.stop) // simulate a crash: no more renewals, no release
	<-a.done
	wantHeld(t, j, "engine-b", "engine-a")
	deadline := time.Now().Add(5 * leaseTTL)
	for {
		b, err := acquire(t, j, "engine-b")
		if err == nil {
			b.Release(ctxT(t))
			return
		}
		if !errors.Is(err, ErrLeaseHeld) || time.Now().After(deadline) {
			t.Fatalf("successor could not take the expired lease: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// If the lease is taken away (deleted by an operator, or expired and re-acquired), the old
// holder sees Lost() within one renewal period and its Release does not delete the new lease.
func TestLeaseLostIsDetectedAndNotReleased(t *testing.T) {
	j := connect(t, DefaultStreams())
	logs := captureLog(t)
	a := mustAcquire(t, j, "engine-a")
	if err := a.kv.Delete(ctxT(t), "engine"); err != nil { // operator removes it
		t.Fatal(err)
	}
	b := mustAcquire(t, j, "engine-b")
	defer b.Release(ctxT(t))
	select {
	case <-a.Lost():
	case <-time.After(2 * leaseTTL):
		t.Fatal("old holder did not notice the lease was lost")
	}
	if err := a.Release(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	wantHeld(t, j, "engine-c", "engine-b")
	if !strings.Contains(logs.String(), "lease lost") || !strings.Contains(logs.String(), "revision changed") {
		t.Errorf("loss not logged: %s", logs.String())
	}
}

func TestLeaseBucketHasTTL(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	defer a.Release(ctxT(t))
	st, err := a.kv.Status(context.Background())
	if err != nil || st.TTL() != leaseTTL || st.History() != 1 {
		t.Fatalf("bucket status %+v %v", st, err)
	}
}
