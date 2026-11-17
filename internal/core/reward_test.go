package core

import "testing"

func snapshot(total, lastLogin int64, day, state string, baseline, receipt bool) RewardSnapshot {
	return RewardSnapshot{Total: total, LastLogin: lastLogin, DayKey: day, State: state, Baseline: baseline, Receipt: receipt}
}

func TestEvaluateRewardAdoptsFirstBaseline(t *testing.T) {
	day := "2026-11-15"
	for name, tc := range map[string]struct {
		evidence RewardEvidence
		want     string
	}{
		"pending":         {RewardEvidence{Total: 62500000, TotalSet: true, LastLoginSet: true}, RewardStatePendingLogin},
		"logged in today": {RewardEvidence{Total: 62500000, TotalSet: true, LastLogin: 1794700000, LastLoginSet: true, LoginToday: true}, RewardStateUnknown},
		"receipt only":    {RewardEvidence{Total: 62500000, TotalSet: true, Receipt: true}, RewardStateUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			out := EvaluateReward(RewardSnapshot{}, tc.evidence, day)
			if out.Status != tc.want || out.QuotaAwarded != 0 || !out.Snapshot.Baseline {
				t.Fatal(out)
			}
			if out.Snapshot.Total != 62500000 || out.Snapshot.DayKey != day {
				t.Fatal(out.Snapshot)
			}
		})
	}
}

func TestEvaluateRewardConfirmsRiseOnlyWithTodaysLoginSignal(t *testing.T) {
	day := "2026-11-15"
	cur := snapshot(62500000, 0, day, RewardStatePendingLogin, true, false)
	confirmed := EvaluateReward(cur, RewardEvidence{Total: 75000000, TotalSet: true, LastLogin: 1794700000, LastLoginSet: true, LoginToday: true}, day)
	if confirmed.Status != "success" || confirmed.QuotaAwarded != 12500000 || confirmed.Snapshot.State != RewardStateSuccess {
		t.Fatal(confirmed)
	}
	unattributed := EvaluateReward(cur, RewardEvidence{Total: 75000000, TotalSet: true, LastLoginSet: true}, day)
	if unattributed.Status != RewardStatePendingLogin || unattributed.QuotaAwarded != 0 {
		t.Fatal("a rise without today's login must not be claimed as today's reward", unattributed)
	}
	// The rise is adopted so it cannot be counted twice once a real login happens.
	if unattributed.Snapshot.Total != 75000000 {
		t.Fatal(unattributed.Snapshot)
	}
	receipt := EvaluateReward(cur, RewardEvidence{Total: 75000000, TotalSet: true, Receipt: true}, day)
	if receipt.Status != "success" || receipt.QuotaAwarded != 12500000 {
		t.Fatal(receipt)
	}
}

func TestEvaluateRewardUnknownCanBeConfirmedLaterToday(t *testing.T) {
	day := "2026-11-15"
	cur := snapshot(62500000, 1794700000, day, RewardStateUnknown, true, false)
	out := EvaluateReward(cur, RewardEvidence{Total: 75000000, TotalSet: true, LastLogin: 1794700000, LastLoginSet: true, LoginToday: true}, day)
	if out.Status != "success" || out.QuotaAwarded != 12500000 {
		t.Fatal(out)
	}
	same := EvaluateReward(cur, RewardEvidence{Total: 62500000, TotalSet: true, LastLoginSet: true}, day)
	if same.Status != RewardStateUnknown {
		t.Fatal("an ambiguous login today must stay unknown, not regress to pending", same)
	}
}

func TestEvaluateRewardSameDaySuccessIsAlready(t *testing.T) {
	day := "2026-11-15"
	cur := snapshot(75000000, 1794700000, day, RewardStateSuccess, true, true)
	out := EvaluateReward(cur, RewardEvidence{Total: 75000000, TotalSet: true, LastLoginSet: true}, day)
	if out.Status != "already" || out.Snapshot.State != RewardStateSuccess {
		t.Fatal(out)
	}
}

func TestEvaluateRewardNewDayResetsExpectation(t *testing.T) {
	cur := snapshot(75000000, 1794700000, "2026-11-14", RewardStateSuccess, true, true)
	out := EvaluateReward(cur, RewardEvidence{Total: 75000000, TotalSet: true, LastLoginSet: true}, "2026-11-15")
	if out.Status != RewardStatePendingLogin || out.Snapshot.State != RewardStatePendingLogin || out.Snapshot.Receipt {
		t.Fatal(out)
	}
	if out.Snapshot.Total != 75000000 || !out.Snapshot.Baseline {
		t.Fatal(out.Snapshot)
	}
}

func TestEvaluateRewardZeroBaselineIsValid(t *testing.T) {
	day := "2026-11-15"
	cur := snapshot(0, 0, day, RewardStatePendingLogin, true, false)
	out := EvaluateReward(cur, RewardEvidence{Total: 12500000, TotalSet: true, LastLogin: 1794700000, LastLoginSet: true, LoginToday: true}, day)
	if out.Status != "success" || out.QuotaAwarded != 12500000 {
		t.Fatal(out)
	}
	// A record with no baseline flag (legacy) still adopts the first value.
	legacy := EvaluateReward(RewardSnapshot{}, RewardEvidence{Total: 12500000, TotalSet: true, LastLogin: 1794700000, LastLoginSet: true, LoginToday: true}, day)
	if legacy.Status != RewardStateUnknown || legacy.QuotaAwarded != 0 {
		t.Fatal(legacy)
	}
}

func TestEvaluateRewardMissingFieldsKeepPriorSnapshot(t *testing.T) {
	day := "2026-11-15"
	cur := snapshot(62500000, 1794000000, day, RewardStatePendingLogin, true, false)
	out := EvaluateReward(cur, RewardEvidence{}, day)
	if out.Snapshot.Total != 62500000 || out.Snapshot.LastLogin != 1794000000 {
		t.Fatal(out.Snapshot)
	}
	if out.Status != RewardStatePendingLogin {
		t.Fatal(out.Status)
	}
}
