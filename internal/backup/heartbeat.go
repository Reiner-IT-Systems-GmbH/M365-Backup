package backup

import (
	"fmt"
	"time"
)

const (
	heartbeatInterval       = 5 * time.Second
	heartbeatEveryNExchange = 250
	heartbeatEveryNOneDrive = 250
)

type progressHeartbeat struct {
	interval time.Duration
	everyN   int
	last     time.Time
}

func newProgressHeartbeat(interval time.Duration, everyN int) progressHeartbeat {
	return progressHeartbeat{interval: interval, everyN: everyN, last: time.Now()}
}

func (h *progressHeartbeat) shouldFire(n int) bool {
	if n == 0 {
		return false
	}
	if h.everyN > 0 && n%h.everyN == 0 {
		h.last = time.Now()
		return true
	}
	if time.Since(h.last) >= h.interval {
		h.last = time.Now()
		return true
	}
	return false
}

type folderProgress struct {
	hb    progressHeartbeat
	start time.Time
}

func newFolderProgress() folderProgress {
	return folderProgress{
		hb:    newProgressHeartbeat(heartbeatInterval, heartbeatEveryNExchange),
		start: time.Now(),
	}
}

func (fp *folderProgress) maybeEmit(res *Result, live func(string), upn, folderName string, n, skippedStored int) {
	if !fp.hb.shouldFire(n) {
		return
	}
	emitProgress(res, live, fmt.Sprintf("%s / %s: %s", upn, folderName, progressDetail(n, skippedStored, fp.start)))
}

type driveProgress struct {
	hb    progressHeartbeat
	start time.Time
}

func newDriveProgress() driveProgress {
	return driveProgress{
		hb:    newProgressHeartbeat(heartbeatInterval, heartbeatEveryNOneDrive),
		start: time.Now(),
	}
}

func (dp *driveProgress) maybeEmit(res *Result, live func(string), upn string, n, skippedStored int) {
	if !dp.hb.shouldFire(n) {
		return
	}
	emitProgress(res, live, fmt.Sprintf("%s: %s", upn, progressDetail(n, skippedStored, dp.start)))
}

func progressDetail(n, skippedStored int, start time.Time) string {
	downloaded := n - skippedStored
	if downloaded < 0 {
		downloaded = 0
	}
	elapsed := time.Since(start).Round(time.Second)
	return fmt.Sprintf("progress %d change(s) (%d skipped, %d downloaded, elapsed %s)…",
		n, skippedStored, downloaded, elapsed)
}

func emitProgress(res *Result, live func(string), msg string) {
	if live != nil {
		live(msg)
		return
	}
	res.Info(msg)
}
