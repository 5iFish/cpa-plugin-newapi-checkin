package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"fmt"
	"sort"
	"time"
)

func PlanDaily(id string, date time.Time, settings config.Settings, randomIntn func(int) int) (time.Time, error) {
	if e := config.Validate(settings); e != nil {
		return time.Time{}, e
	}
	loc, _ := config.Location(settings)
	d := date.In(loc)
	y, m, day := d.Date()
	var hour, minute int
	_, _ = fmt.Sscanf(settings.CheckinAt, "%d:%d", &hour, &minute)
	wanted := hour*60 + minute
	start := time.Date(y, m, day, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	bestMinute := 24 * 60
	var selected time.Time
	// Walk actual instants: missing wall-clock minutes choose the next valid minute,
	// and repeated wall-clock minutes retain their first occurrence.
	for at := start.Add(-3 * time.Hour); at.Before(end.Add(3 * time.Hour)); at = at.Add(time.Minute) {
		local := at.In(loc)
		yy, mm, dd := local.Date()
		if yy != y || mm != m || dd != day {
			continue
		}
		n := local.Hour()*60 + local.Minute()
		if n >= wanted && n < bestMinute {
			bestMinute = n
			selected = at
		}
	}
	if selected.IsZero() {
		selected = end.Add(-time.Minute)
	}
	if settings.JitterMinutes > 0 {
		n := settings.JitterMinutes*2 + 1
		selected = selected.Add(time.Duration(randomIntn(n)-settings.JitterMinutes) * time.Minute)
	}
	if selected.Before(start) {
		selected = start
	}
	if !selected.Before(end) {
		selected = end.Add(-time.Second)
	}
	return selected.UTC(), nil
}
func (s *Scheduler) Tick(ctx context.Context, settings config.Settings, startup bool) error {
	if !settings.Enabled {
		return nil
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	loc, e := config.Location(settings)
	if e != nil {
		return e
	}
	now := s.clock.Now().UTC()
	date := now.In(loc).Format("2006-01-02")
	return s.state.Update(func(v *state.Snapshot) error {
		ids := make([]string, 0, len(v.Accounts))
		for id := range v.Accounts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	accountsLoop:
		for _, id := range ids {
			a := v.Accounts[id]
			if a.Status != "ready" || v.DeletePending[id] {
				continue
			}
			entry, exists := v.Schedules[id]
			if !exists || entry.ScheduleDate != date {
				if exists && entry.ScheduleDate != date {
					if old, ok := v.WorkItems[entry.WorkID]; ok && old.CreatedAt.IsZero() {
						old.CreatedAt = entry.PlannedAt
						v.WorkItems[entry.WorkID] = old
					}
				}
				planned, e := PlanDaily(id, now, settings, s.randomIntn)
				if e != nil {
					return e
				}
				if startup && !planned.After(now) {
					planned = now.Add(time.Duration(120+s.randomIntn(181)) * time.Second)
				}
				entry = state.ScheduleEntry{AccountID: id, ScheduleDate: date, PlannedAt: planned}
				v.Schedules[id] = entry
			} else if startup && entry.WorkID == "" && !entry.PlannedAt.After(now) {
				// Persisted overdue time is moved only once on this startup; subsequent ticks use it.
				entry.PlannedAt = now.Add(time.Duration(120+s.randomIntn(181)) * time.Second)
				v.Schedules[id] = entry
			}
			if entry.WorkID != "" || entry.PlannedAt.After(now) {
				continue
			}
			workID := ""
			nextAttemptAt := now
			needsReconcile := false
			for wid, w := range v.WorkItems {
				if w.AccountID == id && active(w.Status) {
					if !w.CreatedAt.IsZero() && w.CreatedAt.In(loc).Format("2006-01-02") < date {
						// Wait for an in-flight operation; then the new daily round gets
						// its own budget while retaining the old retry deadline.
						if w.Status == "running" {
							continue accountsLoop
						}
						if w.NextAttemptAt.After(nextAttemptAt) {
							nextAttemptAt = w.NextAttemptAt
						}
						needsReconcile = needsReconcile || w.NeedsReconcile
						w.Status = "canceled"
						v.WorkItems[wid] = w
						continue
					}
					workID = wid
					break
				}
			}
			if workID == "" {
				workID = randomID()
				v.WorkItems[workID] = state.WorkItem{ID: workID, AccountID: id, CreatedAt: now, Status: "queued", NextAttemptAt: nextAttemptAt, NeedsReconcile: needsReconcile, Generation: a.LastGeneration}
			}
			jobID := randomID()
			v.Jobs[jobID] = state.Job{ID: jobID, Trigger: "scheduled", Status: "queued", CreatedAt: now, WorkIDs: []string{workID}}
			entry.WorkID = workID
			v.Schedules[id] = entry
		}
		v.UpdateJobs()
		return nil
	})
}
