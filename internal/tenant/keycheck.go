package tenant

import (
	"context"
	"log/slog"
	"time"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/notification"
)

// CheckSecretExpiry runs daily and emits key expiry notifications.
func CheckSecretExpiry(ctx context.Context, database *db.DB, notifier *notification.Service, log *slog.Logger) {
	tenants, err := database.ListTenants(ctx)
	if err != nil {
		log.Error("keycheck list tenants", "err", err)
		return
	}
	for _, t := range tenants {
		if t.SecretExpires.IsZero() {
			log.Warn("tenant has no secret expiry set", "tenant", t.Name, "id", t.ID)
			continue
		}
		daysLeft := int(time.Until(t.SecretExpires).Hours() / 24)
		switch {
		case daysLeft < 0:
			_ = notifier.Send(ctx, notification.Event{
				Type:     notification.EventKeyExpired,
				TenantID: t.ID,
				Subject:  "Client secret expired: " + t.Name,
				Body:     "Azure app client secret for tenant " + t.Name + " has expired. Renew immediately.",
			})
		case daysLeft <= 7:
			_ = notifier.Send(ctx, notification.Event{
				Type:     notification.EventKeyExpiry7D,
				TenantID: t.ID,
				Subject:  "Client secret expires in ≤7 days: " + t.Name,
				Body:     "Renew the Azure app client secret soon.",
			})
		case daysLeft <= 30:
			_ = notifier.Send(ctx, notification.Event{
				Type:     notification.EventKeyExpiry30D,
				TenantID: t.ID,
				Subject:  "Client secret expires in ≤30 days: " + t.Name,
				Body:     "Plan renewal of the Azure app client secret.",
			})
		}
	}
}
