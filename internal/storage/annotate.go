package storage

import (
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

// LatestSnapshotForService returns the newest snapshot for a service, or nil.
func LatestSnapshotForService(snaps []SnapshotInfo, service string) *SnapshotInfo {
	service = strings.ToLower(strings.TrimSpace(service))
	if service == "" {
		return nil
	}
	for i := range snaps {
		svc := snaps[i].Service
		if svc == "" {
			svc = InferServiceFromSource(snaps[i].Source)
		}
		if strings.EqualFold(svc, service) {
			return &snaps[i]
		}
	}
	return nil
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
