package credential

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"errors"
	"time"
)

type pendingSave struct {
	account  core.Account
	revision Revision
}
type Broker struct {
	repo    *Store
	state   *state.Store
	client  *newapi.Client
	gate    *core.Gate
	now     func() time.Time
	pending map[string]pendingSave
}

func NewBroker(repo *Store, s *state.Store, client *newapi.Client, gate *core.Gate, now func() time.Time) *Broker {
	if now == nil {
		now = time.Now
	}
	return &Broker{repo: repo, state: s, client: client, gate: gate, now: now, pending: map[string]pendingSave{}}
}

// Forget is called only inside the shared gate, after an explicit rebind or deletion.
func (b *Broker) Forget(id string) { delete(b.pending, id) }
func (b *Broker) status(id, status string) error {
	return b.state.Update(func(s *state.Snapshot) error {
		a, ok := s.Accounts[id]
		if ok {
			a.Status = status
			s.Accounts[id] = a
		}
		return nil
	})
}
func (b *Broker) finishPending(id string, a core.Account) error {
	return b.state.Update(func(s *state.Snapshot) error {
		pending := s.RefreshPending[id]
		for key, work := range s.WorkItems {
			if work.AccountID == id && work.Generation == pending.OldGeneration && (work.Status == "running" || work.Status == "retry" || work.Status == "queued") {
				work.Generation = a.Credential.Generation
				s.WorkItems[key] = work
			}
		}
		delete(s.RefreshPending, id)
		r := s.Accounts[id]
		r.LastGeneration = a.Credential.Generation
		if r.Status == "persistence_error" || r.Status == "refresh_outcome_unknown" {
			r.Status = "ready"
		}
		s.Accounts[id] = r
		return nil
	})
}
func (b *Broker) Recover(ctx context.Context) error {
	return b.gate.Do(ctx, func() error {
		for id, p := range b.state.Read().RefreshPending {
			a, _, e := b.repo.Load(ctx, id)
			if core.ErrorKind(e) == "host_unavailable" {
				return e
			}
			if e == nil && a.Credential.Generation > p.OldGeneration && !a.Disabled {
				if e = b.finishPending(id, a); e != nil {
					return e
				}
			} else {
				if e = b.status(id, "refresh_outcome_unknown"); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
func (b *Broker) Use(ctx context.Context, id string, action func(core.Account, string) error) error {
	return b.WithAccount(ctx, id, func(use UseCredential) error { return use(action) })
}

// UseCredential is valid only during its enclosing WithAccount operation.
type UseCredential func(func(core.Account, string) error) error

// WithAccount serializes validation, credential use and final state changes with
// binding and deletion. Callers must neither retain use nor reenter the gate.
func (b *Broker) WithAccount(ctx context.Context, id string, operation func(UseCredential) error) error {
	return b.gate.Do(ctx, func() error {
		return operation(func(action func(core.Account, string) error) error {
			return b.useLocked(ctx, id, action)
		})
	})
}

func (b *Broker) useLocked(ctx context.Context, id string, action func(core.Account, string) error) error {
	snapshot := b.state.Read()
	if snapshot.DeletePending[id] {
		return core.Error("deleted")
	}
	if p, ok := b.pending[id]; ok {
		if e := b.repo.Save(ctx, p.account, p.revision); e != nil {
			return e
		}
		if e := b.finishPending(id, p.account); e != nil {
			return e
		}
		delete(b.pending, id)
	}
	a, rev, e := b.repo.Load(ctx, id)
	if e != nil {
		return e
	}
	if a.Disabled {
		return core.Error("disabled")
	}
	if p, ok := b.state.Read().RefreshPending[id]; ok {
		if a.Credential.Generation > p.OldGeneration {
			if e = b.finishPending(id, a); e != nil {
				return e
			}
		} else {
			return core.Error("refresh_outcome_unknown")
		}
	}
	status := b.state.Read().Accounts[id].Status
	if status == "refresh_outcome_unknown" || status == "external_change" {
		return core.Error(status)
	}
	refreshed := false
	if a.Credential.Kind == "session" {
		if a.Credential.SessionExpiresAt <= b.now().Unix() {
			return core.Error("credential_expired")
		}
		if a.Credential.AccessExpiresAt <= b.now().Add(time.Minute).Unix() {
			a, rev, e = b.refresh(ctx, a, rev)
			if e != nil {
				return e
			}
			refreshed = true
		}
	}
	bearer := func(a core.Account) string {
		if a.Credential.Kind == "session" {
			return a.Credential.AccessToken
		}
		return a.Credential.Token
	}
	if e = b.repo.Check(ctx, id, rev); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	e = action(a, bearer(a))
	var api *core.APIError
	if !refreshed && a.Credential.Kind == "session" && errors.As(e, &api) && api.HTTPStatus == 401 && api.Code == "AUTH_TOKEN_EXPIRED" {
		a, rev, e = b.refresh(ctx, a, rev)
		if e != nil {
			return e
		}
		if e = b.repo.Check(ctx, id, rev); e != nil {
			return e
		}
		return action(a, bearer(a))
	}
	return e
}
func (b *Broker) refresh(ctx context.Context, a core.Account, rev Revision) (core.Account, Revision, error) {
	fail := func(e error) (core.Account, Revision, error) {
		status := core.ErrorKind(e)
		if saveErr := b.status(a.ID, status); saveErr != nil {
			e = saveErr
		}
		return a, rev, e
	}
	if e := b.repo.Check(ctx, a.ID, rev); e != nil {
		return fail(e)
	}
	if e := b.state.Update(func(s *state.Snapshot) error {
		s.RefreshPending[a.ID] = state.RefreshPending{AccountID: a.ID, OldGeneration: a.Credential.Generation, StartedAt: b.now().UTC()}
		return nil
	}); e != nil {
		return fail(e)
	}
	bundle, e := b.client.Refresh(ctx, a.Site, a.Credential.RefreshToken)
	if e != nil {
		var api *core.APIError
		definite := errors.As(e, &api) && api.HTTPStatus >= 400 && api.HTTPStatus < 500 && api.Code != ""
		if definite {
			if saveErr := b.state.Update(func(s *state.Snapshot) error { delete(s.RefreshPending, a.ID); return nil }); saveErr != nil {
				return fail(saveErr)
			}
			return fail(e)
		}
		return fail(core.Error("refresh_outcome_unknown"))
	}
	if bundle.Identity.ID != a.UpstreamUserID || bundle.SessionID != a.Credential.SessionID {
		return fail(core.Error("refresh_outcome_unknown"))
	}
	next := a
	next.Credential.AccessToken = bundle.AccessToken
	next.Credential.RefreshToken = bundle.RefreshToken
	next.Credential.AccessExpiresAt = bundle.AccessExpiresAt
	next.Credential.Generation++
	if bundle.SessionExpiresAt < next.Credential.SessionExpiresAt {
		next.Credential.SessionExpiresAt = bundle.SessionExpiresAt
	}
	b.pending[a.ID] = pendingSave{next, rev}
	if e = b.repo.Save(ctx, next, rev); e != nil {
		return fail(e)
	}
	if e = b.finishPending(a.ID, next); e != nil {
		return fail(e)
	}
	delete(b.pending, a.ID)
	saved, newRev, e := b.repo.Load(ctx, a.ID)
	if e != nil {
		return fail(core.Error("persistence_error"))
	}
	return saved, newRev, nil
}
