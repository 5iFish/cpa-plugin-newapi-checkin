package state

import (
	"testing"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

func TestSnapshotCarriesStatsCache(t *testing.T) {
	var s Snapshot
	s.init()
	if s.Stats == nil {
		t.Fatal("stats cache map not initialised")
	}
	s.Stats["7d"] = core.StatsSummary{Range: "7d", Models: []core.StatsModel{{Model: "m", TotalTokens: 5}}, Failed: map[string]string{}}
	if got := s.Stats["7d"]; got.Models[0].Model != "m" || got.Range != "7d" {
		t.Fatalf("stats lost: %+v", got)
	}
}
