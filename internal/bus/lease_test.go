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
	a.stopOnce.Do(func() { close(a.stop) }) // simulate a crash: no more renewals, no release
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

// A refused process configured with another TTL must not change the running holder's expiry.
func TestLeaseTTLMismatchRefusedWithoutTouchingBucket(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	defer a.Release(ctxT(t))
	_, err := j.AcquireLease(ctxT(t), "test_lease", "engine", "engine-b", 5*leaseTTL)
	if err == nil || errors.Is(err, ErrLeaseHeld) || !strings.Contains(err.Error(), "has TTL 1s but this process is configured with 5s") {
		t.Fatalf("want TTL mismatch error, got %v", err)
	}
	st, _ := a.kv.Status(ctxT(t))
	if st.TTL() != leaseTTL {
		t.Fatalf("bucket TTL changed to %s", st.TTL())
	}
	time.Sleep(2 * leaseTTL)
	if a.Valid() != nil || isLost(a) {
		t.Fatal("holder lost its lease after a refused process tried another TTL")
	}
	if _, err := j.AcquireLease(ctxT(t), "test_lease", "engine", "x", 500*time.Millisecond); err == nil {
		t.Fatal("TTL below the minimum accepted")
	}
}

// A renewal that succeeded on the server but whose reply was lost leaves a stale revision;
// the holder must recognise its own value and continue, not report the lease lost.
func TestLeaseRenewalWithLostReplyIsAdopted(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	defer a.Release(ctxT(t))
	a.stopOnce.Do(func() { close(a.stop) }) // drive renewals by hand
	<-a.done
	e, _ := a.kv.Get(ctxT(t), "engine")
	if _, err := a.kv.Update(ctxT(t), "engine", e.Value(), a.rev); err != nil { // the "lost reply" write
		t.Fatal(err)
	}
	if lost, reason := a.renew(e.Value()); lost {
		t.Fatalf("own earlier renewal treated as loss: %s", reason)
	}
	if lost, reason := a.renew(e.Value()); lost { // and the adopted revision works for the next one
		t.Fatalf("renewal after adopting failed: %s", reason)
	}
}

// Valid() turns false before the server can expire the key, even with no renewal errors seen
// (e.g. the process was paused).
func TestLeaseValidDeadline(t *testing.T) {
	j := connect(t, DefaultStreams())
	a := mustAcquire(t, j, "engine-a")
	if a.Valid() != nil {
		t.Fatal("fresh lease invalid")
	}
	a.stopOnce.Do(func() { close(a.stop) }) // a paused process: no renewals
	<-a.done
	time.Sleep(leaseTTL * 3 / 4)
	if err := a.Valid(); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("lease still valid 3/4 TTL after the last renewal: %v", err)
	}
	wantHeld(t, j, "engine-b", "engine-a") // the key itself has not expired yet
}
