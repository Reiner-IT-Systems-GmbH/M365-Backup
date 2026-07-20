package storage

import (
	"encoding/json"
	"fmt"
	"time"
)

// RetentionPolicy is Synology-style Smart Recycle for snapshots (per service).
// Within KeepHours every snapshot is kept; beyond that only one per day/week/month/year.
type RetentionPolicy struct {
	Enabled     bool `json:"enabled"`
	KeepHours   int  `json:"keep_hours"`    // keep all versions in this window (default 24)
	KeepDaily   int  `json:"keep_daily"`    // last N days: newest per day (default 7)
	KeepWeekly  int  `json:"keep_weekly"`   // last N weeks: newest per week (default 4)
	KeepMonthly int  `json:"keep_monthly"`  // last N months: newest per month (default 6)
	KeepYearly  int  `json:"keep_yearly"`   // last N years: newest per year (default 2)
	KeepMin     int  `json:"keep_min"`      // always keep at least N newest (default 3)
	PSTKeepRuns int  `json:"pst_keep_runs"` // keep last N PST export runs (default 5)
}

// DefaultRetention matches a typical Synology Smart Recycle profile.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{
		Enabled:     true,
		KeepHours:   24,
		KeepDaily:   7,
		KeepWeekly:  4,
		KeepMonthly: 6,
		KeepYearly:  2,
		KeepMin:     3,
		PSTKeepRuns: 5,
	}
}

// ParseRetentionJSON decodes policy JSON; empty → defaults.
func ParseRetentionJSON(s string) RetentionPolicy {
	p := DefaultRetention()
	if s == "" {
		return p
	}
	_ = json.Unmarshal([]byte(s), &p)
	p.normalize()
	return p
}

func (p RetentionPolicy) ToJSON() string {
	p.normalize()
	b, _ := json.Marshal(p)
	return string(b)
}

func (p *RetentionPolicy) normalize() {
	if p.KeepHours < 0 {
		p.KeepHours = 0
	}
	if p.KeepDaily < 0 {
		p.KeepDaily = 0
	}
	if p.KeepWeekly < 0 {
		p.KeepWeekly = 0
	}
	if p.KeepMonthly < 0 {
		p.KeepMonthly = 0
	}
	if p.KeepYearly < 0 {
		p.KeepYearly = 0
	}
	if p.KeepMin < 1 {
		p.KeepMin = 1
	}
	if p.PSTKeepRuns < 1 {
		p.PSTKeepRuns = 1
	}
}

// SelectSmartKeepIDs returns snapshot IDs to retain (newest-first input).
// Keep set = union of: KeepMin newest, all in KeepHours window, newest per day/week/month/year slot.
func SelectSmartKeepIDs(snaps []SnapshotInfo, policy RetentionPolicy, now time.Time) map[string]bool {
	policy.normalize()
	keep := map[string]bool{}
	if len(snaps) == 0 {
		return keep
	}
	if !policy.Enabled {
		for i := 0; i < len(snaps) && i < policy.KeepMin; i++ {
			keep[snaps[i].ID] = true
		}
		return keep
	}

	now = now.UTC()
	hourCutoff := now.Add(-time.Duration(policy.KeepHours) * time.Hour)

	for i := 0; i < len(snaps) && i < policy.KeepMin; i++ {
		keep[snaps[i].ID] = true
	}
	for _, sn := range snaps {
		if !sn.CreatedAt.UTC().Before(hourCutoff) {
			keep[sn.ID] = true
		}
	}

	seenDay := map[string]bool{}
	seenWeek := map[string]bool{}
	seenMonth := map[string]bool{}
	seenYear := map[string]bool{}

	for _, sn := range snaps {
		t := sn.CreatedAt.UTC()
		dayKey := t.Format("2006-01-02")
		y, week := t.ISOWeek()
		weekKey := fmt.Sprintf("%d-W%02d", y, week)
		monthKey := t.Format("2006-01")
		yearKey := t.Format("2006")

		if policy.KeepDaily > 0 && daysBetween(t, now) < policy.KeepDaily && !seenDay[dayKey] {
			seenDay[dayKey] = true
			keep[sn.ID] = true
		}
		if policy.KeepWeekly > 0 && weeksBetween(t, now) < policy.KeepWeekly && !seenWeek[weekKey] {
			seenWeek[weekKey] = true
			keep[sn.ID] = true
		}
		if policy.KeepMonthly > 0 && monthsBetween(t, now) < policy.KeepMonthly && !seenMonth[monthKey] {
			seenMonth[monthKey] = true
			keep[sn.ID] = true
		}
		if policy.KeepYearly > 0 && (now.Year()-t.Year()) < policy.KeepYearly && !seenYear[yearKey] {
			seenYear[yearKey] = true
			keep[sn.ID] = true
		}
	}
	return keep
}

func daysBetween(past, now time.Time) int {
	p := time.Date(past.Year(), past.Month(), past.Day(), 0, 0, 0, 0, time.UTC)
	n := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(n.Sub(p).Hours() / 24)
}

func weeksBetween(past, now time.Time) int {
	return daysBetween(past, now) / 7
}

func monthsBetween(past, now time.Time) int {
	return (now.Year()-past.Year())*12 + int(now.Month()) - int(past.Month())
}
