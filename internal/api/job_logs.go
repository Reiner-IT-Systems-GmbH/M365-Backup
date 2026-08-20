package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rhw/m365backup/internal/db"
)

func jobLogCursor(logs []db.JobLog) (id string, at time.Time) {
	if len(logs) == 0 {
		return "", time.Time{}
	}
	last := logs[len(logs)-1]
	return last.ID, last.CreatedAt
}

func jobLogViewData(logs []db.JobLog, total int) map[string]any {
	lastID, lastAt := jobLogCursor(logs)
	return map[string]any{
		"Logs":         logs,
		"LogTotal":     total,
		"LogTailShown": len(logs),
		"LastLogID":    lastID,
		"LastLogAtMS":  lastAt.UnixMilli(),
	}
}

func parseJobLogCursor(r *http.Request) (afterID string, afterAt time.Time) {
	afterID = r.URL.Query().Get("after")
	raw := r.URL.Query().Get("after_at")
	if raw == "" {
		return afterID, time.Time{}
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return afterID, time.UnixMilli(ms).UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return afterID, t.UTC()
	}
	return afterID, time.Time{}
}

func mergeJobLogView(dst map[string]any, logs []db.JobLog, total int) {
	for k, v := range jobLogViewData(logs, total) {
		dst[k] = v
	}
}

func jobRunning(status string) bool {
	return status == "running" || status == "queued"
}
