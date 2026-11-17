package core

// RewardSnapshot is the non-secret evidence persisted for a login-reward
// account. Baseline distinguishes a real zero balance from a record that was
// written before reward tracking existed.
type RewardSnapshot struct {
	Total     int64
	LastLogin int64
	DayKey    string
	State     string
	Baseline  bool
	Receipt   bool
}

// RewardEvidence is read from /api/user/self and, when available, the OAuth
// callback. Presence flags keep a legitimate zero distinct from a missing field.
type RewardEvidence struct {
	Total        int64
	TotalSet     bool
	LastLogin    int64
	LastLoginSet bool
	LoginToday   bool
	Receipt      bool
}

// RewardOutcome is the new snapshot plus the status to show for this attempt.
// QuotaAwarded is an inferred difference, never a server-reported date.
type RewardOutcome struct {
	Snapshot     RewardSnapshot
	Status       string
	QuotaAwarded int64
}

// BaselineKnown distinguishes a stored zero baseline from a record written
// before reward tracking existed. Any non-zero evidence field also counts.
func (s RewardSnapshot) BaselineKnown() bool {
	return s.Baseline || s.Total != 0 || s.LastLogin != 0 || s.DayKey != "" || s.State != ""
}

// EvaluateReward decides whether today's login reward can be claimed. A quota
// rise alone is not enough: it must be accompanied by a login signal from today
// (last_login_time or a callback receipt), otherwise an unrelated or delayed
// grant would be misattributed. A rise without that signal is adopted into the
// baseline so it cannot be counted again.
func EvaluateReward(cur RewardSnapshot, ev RewardEvidence, dayKey string) RewardOutcome {
	next := cur
	if ev.TotalSet {
		next.Total = ev.Total
	}
	if ev.LastLoginSet {
		next.LastLogin = ev.LastLogin
	}
	sameDay := cur.DayKey == dayKey
	if !sameDay {
		next.Receipt = false
	}
	if ev.Receipt {
		next.Receipt = true
	}
	next.DayKey = dayKey
	// A login already recorded today keeps its signal for later evidence
	// queries, even if a retry cannot observe last_login_time again.
	signal := ev.LoginToday || ev.Receipt || (sameDay && (cur.Receipt || cur.State == RewardStateUnknown))

	if sameDay && cur.State == RewardStateSuccess {
		next.State = RewardStateSuccess
		next.Baseline = true
		return RewardOutcome{Snapshot: next, Status: "already"}
	}
	if !cur.BaselineKnown() {
		next.Baseline = true
		if signal {
			next.State = RewardStateUnknown
			return RewardOutcome{Snapshot: next, Status: RewardStateUnknown}
		}
		next.State = RewardStatePendingLogin
		return RewardOutcome{Snapshot: next, Status: RewardStatePendingLogin}
	}
	next.Baseline = true
	if ev.TotalSet && ev.Total > cur.Total {
		if signal {
			next.State = RewardStateSuccess
			return RewardOutcome{Snapshot: next, Status: "success", QuotaAwarded: ev.Total - cur.Total}
		}
		// Adopt the unexplained rise without claiming it, so a later real login
		// starts from the current total rather than counting the same rise twice.
		next.State = RewardStatePendingLogin
		return RewardOutcome{Snapshot: next, Status: RewardStatePendingLogin}
	}
	if signal {
		next.State = RewardStateUnknown
		return RewardOutcome{Snapshot: next, Status: RewardStateUnknown}
	}
	next.State = RewardStatePendingLogin
	return RewardOutcome{Snapshot: next, Status: RewardStatePendingLogin}
}
