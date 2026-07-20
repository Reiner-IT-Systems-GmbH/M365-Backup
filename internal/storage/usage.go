package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ServiceUsage is per-service disk accounting (for UI + billing APIs).
type ServiceUsage struct {
	Service        string `json:"service"`
	SyncBytes      int64  `json:"sync_bytes"`
	SyncHuman      string `json:"sync_human"`
	SnapshotBytes  int64  `json:"snapshot_bytes"`
	SnapshotHuman  string `json:"snapshot_human"`
	SnapshotCount  int    `json:"snapshot_count"`
	TotalBytes     int64  `json:"total_bytes"`
	TotalHuman     string `json:"total_human"`
}

// UserUsage is per-mailbox / per-drive sync usage (live tree).
type UserUsage struct {
	User    string  `json:"user"`
	Service string  `json:"service"`
	Bytes   int64   `json:"bytes"`
	Human   string  `json:"human"`
	GB      float64 `json:"gb"`
}

// UsageReport is tenant storage usage (roughly `du -hs` of the repo).
type UsageReport struct {
	TenantID       string         `json:"tenant_id,omitempty"`
	RepoPath       string         `json:"repo_path"`
	MeasuredAt     time.Time      `json:"measured_at"`
	TotalBytes     int64          `json:"total_bytes"`
	TotalHuman     string         `json:"total_human"`
	TotalGB        float64        `json:"total_gb"`
	SnapshotsBytes int64          `json:"snapshots_bytes"`
	SnapshotsHuman string         `json:"snapshots_human"`
	SyncBytes      int64          `json:"sync_bytes"`
	SyncHuman      string         `json:"sync_human"`
	OtherBytes     int64          `json:"other_bytes"`
	OtherHuman     string         `json:"other_human"`
	ExportsBytes   int64          `json:"exports_bytes"`
	ExportsHuman   string         `json:"exports_human"`
	ByService      []ServiceUsage `json:"by_service"`
	TopUsers       []UserUsage    `json:"top_users"`
	LargestService string         `json:"largest_service,omitempty"`
	LargestUser    string         `json:"largest_user,omitempty"`
}

// DirSize returns the total size of all regular files under root (like du -sb).
func DirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Missing path is fine (no sync yet).
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return total, err
	}
	return total, nil
}

// FormatBytes formats byte counts similar to `du -h` (binary units).
func FormatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	suffix := []string{"K", "M", "G", "T", "P"}[exp]
	if value >= 10 {
		return fmt.Sprintf("%.0f %sB", value, suffix)
	}
	return fmt.Sprintf("%.1f %sB", value, suffix)
}

// BytesToGB returns GiB with 2 decimal places.
func BytesToGB(n int64) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n) / (1024 * 1024 * 1024)
}

// MeasureUsage walks the tenant repo and returns a billing-friendly usage report.
func (e *Engine) MeasureUsage(repoPath string, snaps []SnapshotInfo) (*UsageReport, error) {
	_ = e
	total, _ := DirSize(repoPath)
	snapDir := RepoDataDir(repoPath)
	snapBytes, _ := DirSize(snapDir)
	syncDir := filepath.Join(repoPath, "sync")
	syncBytes, _ := DirSize(syncDir)
	exportsDir := filepath.Join(repoPath, "exports")
	exportsBytes, _ := DirSize(exportsDir)
	other := total - snapBytes - syncBytes - exportsBytes
	if other < 0 {
		other = 0
	}

	services := []string{"exchange", "onedrive", "teams", "sharepoint"}
	bySnapSvc := map[string]int64{}
	bySnapCount := map[string]int{}
	for _, sn := range snaps {
		svc := strings.ToLower(sn.Service)
		if svc == "" {
			svc = InferServiceFromSource(sn.Source)
		}
		if svc == "" {
			svc = "unknown"
		}
		// Logical snapshot size (Kopia dedup means on-disk bytes are shared across snaps).
		bySnapSvc[svc] += sn.Bytes
		bySnapCount[svc]++
	}

	var byService []ServiceUsage
	var largestSvc string
	var largestSvcBytes int64
	for _, svc := range services {
		sb, _ := DirSize(filepath.Join(syncDir, svc))
		ss := bySnapSvc[svc]
		totalSvc := sb + ss
		u := ServiceUsage{
			Service:       svc,
			SyncBytes:     sb,
			SyncHuman:     FormatBytes(sb),
			SnapshotBytes: ss,
			SnapshotHuman: FormatBytes(ss),
			SnapshotCount: bySnapCount[svc],
			TotalBytes:    totalSvc,
			TotalHuman:    FormatBytes(totalSvc),
		}
		byService = append(byService, u)
		if totalSvc > largestSvcBytes {
			largestSvcBytes = totalSvc
			largestSvc = svc
		}
	}
	if unk := bySnapSvc["unknown"]; unk > 0 {
		byService = append(byService, ServiceUsage{
			Service:       "unknown",
			SnapshotBytes: unk,
			SnapshotHuman: FormatBytes(unk),
			SnapshotCount: bySnapCount["unknown"],
			TotalBytes:    unk,
			TotalHuman:    FormatBytes(unk),
		})
	}

	topUsers := collectTopUsers(syncDir, 20)
	largestUser := ""
	if len(topUsers) > 0 {
		largestUser = topUsers[0].User + " (" + topUsers[0].Human + ")"
	}

	return &UsageReport{
		RepoPath:       repoPath,
		MeasuredAt:     time.Now().UTC(),
		TotalBytes:     total,
		TotalHuman:     FormatBytes(total),
		TotalGB:        round2(BytesToGB(total)),
		SnapshotsBytes: snapBytes,
		SnapshotsHuman: FormatBytes(snapBytes),
		SyncBytes:      syncBytes,
		SyncHuman:      FormatBytes(syncBytes),
		OtherBytes:     other,
		OtherHuman:     FormatBytes(other),
		ExportsBytes:   exportsBytes,
		ExportsHuman:   FormatBytes(exportsBytes),
		ByService:      byService,
		TopUsers:       topUsers,
		LargestService: largestSvc,
		LargestUser:    largestUser,
	}, nil
}

func collectTopUsers(syncDir string, limit int) []UserUsage {
	var out []UserUsage
	for _, svc := range []string{"exchange", "onedrive"} {
		base := filepath.Join(syncDir, svc)
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			name := ent.Name()
			if name == "" || strings.HasPrefix(name, ".") {
				continue
			}
			sz, _ := DirSize(filepath.Join(base, name))
			if sz == 0 {
				continue
			}
			out = append(out, UserUsage{
				User:    name,
				Service: svc,
				Bytes:   sz,
				Human:   FormatBytes(sz),
				GB:      round2(BytesToGB(sz)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
