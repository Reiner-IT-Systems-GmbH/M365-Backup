package api

import (
	"encoding/json"
	"strings"

	"github.com/rhw/m365backup/internal/db"
)

// publicTenant omits encrypted secret material from API responses.
func publicTenant(t db.Tenant) db.Tenant {
	t.ClientSecret = ""
	t.KopiaPassword = ""
	return t
}

func publicTenants(list []db.Tenant) []db.Tenant {
	out := make([]db.Tenant, len(list))
	for i := range list {
		out[i] = publicTenant(list[i])
	}
	return out
}

// publicNotificationSetting redacts secrets inside channel config JSON.
func publicNotificationSetting(st db.NotificationSetting) db.NotificationSetting {
	st.Config = redactNotificationConfig(st.Config)
	return st
}

func publicNotificationSettings(list []db.NotificationSetting) []db.NotificationSetting {
	out := make([]db.NotificationSetting, len(list))
	for i := range list {
		out[i] = publicNotificationSetting(list[i])
	}
	return out
}

func redactNotificationConfig(configJSON string) string {
	if configJSON == "" {
		return configJSON
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(configJSON), &m); err != nil {
		return `{}`
	}
	for _, key := range []string{"password", "app_token", "user_key", "token", "secret"} {
		if _, ok := m[key]; ok {
			m[key] = ""
		}
	}
	if hdrs, ok := m["headers"].(map[string]any); ok {
		for k := range hdrs {
			if isSensitiveHeader(k) {
				hdrs[k] = ""
			}
		}
		m["headers"] = hdrs
	}
	b, err := json.Marshal(m)
	if err != nil {
		return `{}`
	}
	return string(b)
}

func isSensitiveHeader(k string) bool {
	return strings.EqualFold(k, "authorization") ||
		strings.EqualFold(k, "x-api-key") ||
		strings.EqualFold(k, "x-auth-token")
}
