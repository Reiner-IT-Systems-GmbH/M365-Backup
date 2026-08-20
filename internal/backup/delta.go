package backup

import (
	"context"

	"github.com/rhw/m365backup/internal/db"
)

// odataResumeURL prefers a Graph nextLink (mid-folder checkpoint) over the
// final deltaLink so a restart does not replay from item 0.
func odataResumeURL(next, delta *string) string {
	if next != nil && *next != "" {
		return *next
	}
	if delta != nil && *delta != "" {
		return *delta
	}
	return ""
}

func persistDeltaURL(ctx context.Context, tokens TokenStore, tenantID, service, userKey, url string) {
	if url == "" || tokens == nil {
		return
	}
	_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
		TenantID: tenantID, Service: service, UserID: userKey, Token: url,
	})
}
