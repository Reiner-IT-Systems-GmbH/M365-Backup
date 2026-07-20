package tenant_test

import (
	"testing"
	"time"

	"github.com/rhw/m365backup/internal/tenant"
)

func TestConsentStateRoundTrip(t *testing.T) {
	key := "test-master-key-placeholder-not-real"
	state, err := tenant.SignConsentState("tenant-uuid", key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id, err := tenant.VerifyConsentState(state, key)
	if err != nil {
		t.Fatal(err)
	}
	if id != "tenant-uuid" {
		t.Fatalf("got %q", id)
	}
	if _, err := tenant.VerifyConsentState(state, "wrong-key"); err == nil {
		t.Fatal("expected signature failure")
	}
}
