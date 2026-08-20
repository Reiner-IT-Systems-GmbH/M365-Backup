package backup

import (
	"context"
	"sync"
	"testing"

	"github.com/rhw/m365backup/internal/db"
)

func TestOdataResumeURL(t *testing.T) {
	next := "https://graph.microsoft.com/next"
	delta := "https://graph.microsoft.com/delta"
	if got := odataResumeURL(&next, &delta); got != next {
		t.Fatalf("prefer nextLink: %q", got)
	}
	if got := odataResumeURL(nil, &delta); got != delta {
		t.Fatalf("deltaLink: %q", got)
	}
	empty := ""
	if got := odataResumeURL(&empty, nil); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

type memTokens struct {
	mu sync.Mutex
	m  map[string]string
}

func (t *memTokens) key(tenantID, service, userID string) string {
	return tenantID + "\x00" + service + "\x00" + userID
}

func (t *memTokens) GetDeltaToken(_ context.Context, tenantID, service, userID string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		return "", nil
	}
	return t.m[t.key(tenantID, service, userID)], nil
}

func (t *memTokens) UpsertDeltaToken(_ context.Context, tok db.DeltaToken) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]string{}
	}
	t.m[t.key(tok.TenantID, tok.Service, tok.UserID)] = tok.Token
	return nil
}

func TestPersistDeltaURLCheckpointsNextThenDelta(t *testing.T) {
	ctx := context.Background()
	toks := &memTokens{}
	next := "https://graph.microsoft.com/next-page"
	persistDeltaURL(ctx, toks, "ten", "exchange", "user|folder", odataResumeURL(&next, nil))
	got, _ := toks.GetDeltaToken(ctx, "ten", "exchange", "user|folder")
	if got != next {
		t.Fatalf("nextLink checkpoint: %q", got)
	}
	delta := "https://graph.microsoft.com/delta-link"
	persistDeltaURL(ctx, toks, "ten", "exchange", "user|folder", odataResumeURL(nil, &delta))
	got, _ = toks.GetDeltaToken(ctx, "ten", "exchange", "user|folder")
	if got != delta {
		t.Fatalf("deltaLink: %q", got)
	}
}
