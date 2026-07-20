package storage

import (
	"os"
	"path/filepath"
	"strings"
)

// AnnotateServices fills missing Service fields using source paths and an optional
// map of snapshotID → service (e.g. from jobs).
func AnnotateServices(snaps []SnapshotInfo, snapService map[string]string) {
	for i := range snaps {
		if snaps[i].Service != "" {
			continue
		}
		if snapService != nil {
			if svc, ok := snapService[snaps[i].ID]; ok && svc != "" {
				snaps[i].Service = svc
				continue
			}
		}
		snaps[i].Service = InferServiceFromSource(snaps[i].Source)
	}
}

// FilterByService returns snapshots for one service (empty service = all).
func FilterByService(snaps []SnapshotInfo, service string) []SnapshotInfo {
	service = strings.ToLower(strings.TrimSpace(service))
	if service == "" || service == "all" {
		return snaps
	}
	var out []SnapshotInfo
	for _, s := range snaps {
		if strings.EqualFold(s.Service, service) {
			out = append(out, s)
		}
	}
	return out
}

// LiveSyncRoot returns the persistent sync directory for a service, if it exists.
func LiveSyncRoot(repoPath, service string) (string, bool) {
	service = strings.ToLower(strings.TrimSpace(service))
	switch service {
	case "exchange", "onedrive":
		p := filepath.Join(repoPath, "sync", service)
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			return "", false
		}
		return p, true
	default:
		return "", false
	}
}
