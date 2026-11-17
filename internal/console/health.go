package console

import (
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

func (h *Handler) healthStatus() core.HealthSummary {
	if h.accounts != nil {
		return h.accounts.HealthStatus()
	}
	out := core.HealthSummary{Accounts: map[string]core.AccountHealth{}}
	s := h.state.Read()
	for id, health := range s.Health {
		a, exists := s.Accounts[id]
		if !exists || a.Status == "deleted" || s.DeletePending[id] {
			continue
		}
		if !health.SyncedAt.IsZero() && time.Since(health.SyncedAt) >= 5*time.Minute {
			health.Stale = true
		}
		out.Accounts[id] = health
	}
	return out
}
