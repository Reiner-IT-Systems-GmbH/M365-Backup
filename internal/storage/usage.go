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
	Service       string `json:"service"`
	SyncBytes     int64  `json:"sync_bytes"`
	SyncHuman     string `json:"sync_human"`
	SnapshotBytes int64  `json:"snapshot_bytes"`
	SnapshotHuman string `json:"snapshot_human"`
	SnapshotCount int    `json:"snapshot_count"`
	TotalBytes    int64  `json:"total_bytes"`
	TotalHuman    string `json:"total_human"`
}

// UserUsage is per-mailbox / per-drive live catalog usage.
type UserUsage struct {
	User    string  `json:"user"`
	Service string  `json:"service"`
	Bytes   int64   `json:"bytes"`
	Human   string  `json:"human"`
	GB      float64 `json:"gb"`
}

// UsageReport is tenant storage usage (blobs + manifests + exports).
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

func BytesToGB(n int64) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n) / (1024 * 1024 * 1024)
}

// UsageExtras supplies catalog logical sizes (optional).
type UsageExtras struct {
	LiveByService map[string]int64
	SnapByService map[string]int64
	SnapCount     map[string]int
	TopUsers      []UserUsage
}

// MeasureUsage walks blobs + manifests + exports. extras fills per-service logical columns.
func (e *Engine) MeasureUsage(repoPath string, snaps []SnapshotInfo) (*UsageReport, error) {
	return e.MeasureUsageEx(repoPath, snaps, UsageExtras{})
}

func (e *Engine) MeasureUsageEx(repoPath string, snaps []SnapshotInfo, extras UsageExtras) (*UsageReport, error) {
	_ = e
	total, _ := DirSize(repoPath)
	blobBytes, _ := DirSize(BlobsDir(repoPath))
	manBytes, _ := DirSize(ManifestsDir(repoPath))
	snapBytes := blobBytes + manBytes
	exportsDir := filepath.Join(repoPath, "exports")
	exportsBytes, _ := DirSize(exportsDir)
	other := total - snapBytes - exportsBytes
	if other < 0 {
		other = 0
	}

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
		bySnapSvc[svc] += sn.Bytes
		bySnapCount[svc]++
	}
	for svc, n := range extras.SnapByService {
		bySnapSvc[svc] = n
	}
	for svc, n := range extras.SnapCount {
		bySnapCount[svc] = n
	}

	services := []string{"exchange", "onedrive", "teams", "sharepoint"}
	var byService []ServiceUsage
	var largestSvc string
	var largestSvcBytes int64
	for _, svc := range services {
		live := extras.LiveByService[svc]
		ss := bySnapSvc[svc]
		totalSvc := live + ss
		u := ServiceUsage{
			Service:       svc,
			SyncBytes:     live,
			SyncHuman:     FormatBytes(live),
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

	topUsers := extras.TopUsers
	if len(topUsers) > 20 {
		topUsers = topUsers[:20]
	}
	sort.Slice(topUsers, func(i, j int) bool { return topUsers[i].Bytes > topUsers[j].Bytes })
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
		SyncBytes:      sumMap(extras.LiveByService),
		SyncHuman:      FormatBytes(sumMap(extras.LiveByService)),
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

func sumMap(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
