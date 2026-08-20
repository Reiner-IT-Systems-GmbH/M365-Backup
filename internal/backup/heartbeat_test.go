package backup

import (
	"testing"
	"time"
)

func TestProgressHeartbeatEveryN(t *testing.T) {
	hb := newProgressHeartbeat(time.Hour, 250)
	for i := 1; i < 250; i++ {
		if hb.shouldFire(i) {
			t.Fatalf("should not fire at %d", i)
		}
	}
	if !hb.shouldFire(250) {
		t.Fatal("expected fire at 250")
	}
}

func TestProgressHeartbeatTimeBased(t *testing.T) {
	hb := newProgressHeartbeat(10*time.Millisecond, 1000)
	hb.last = time.Now().Add(-20 * time.Millisecond)
	if !hb.shouldFire(1) {
		t.Fatal("expected time-based fire")
	}
}

func TestProgressHeartbeatZero(t *testing.T) {
	hb := newProgressHeartbeat(time.Millisecond, 250)
	if hb.shouldFire(0) {
		t.Fatal("n=0 must not fire")
	}
}

func TestProgressDetail(t *testing.T) {
	start := time.Now().Add(-2 * time.Second)
	msg := progressDetail(1000, 995, start)
	if msg == "" {
		t.Fatal("empty detail")
	}
	if want := "progress 1000 change(s) (995 skipped, 5 downloaded"; msg[:len(want)] != want {
		t.Fatalf("detail=%q want prefix %q", msg, want)
	}
}
