package account

import (
	"context"
	"errors"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"strings"
	"time"
)

type BindInput struct {
	UpstreamUserID            int64  `json:"upstream_user_id"`
	Site                      string `json:"site"`
	Label                     string `json:"label"`
	Channel                   string `json:"channel"`
	Token                     string `json:"token"`
	Username                  string `json:"username"`
	Password                  string `json:"password"`
	RefreshCookie             string `json:"refresh_cookie"`
	AccountID                 string `json:"account_id"`
	DedicatedSessionConfirmed bool   `json:"dedicated_session_confirmed"`
	ExpiresAtHint             *int64 `json:"expires_at_hint"`
}
type Service struct {
	repo   *credential.Store
	state  *state.Store
	client *newapi.Client
	broker *credential.Broker
	gate   *core.Gate
}

func NewService(r *credential.Store, s *state.Store, c *newapi.Client, b *credential.Broker, g *core.Gate) *Service {
	return &Service{r, s, c, b, g}
}
func (s *Service) Bind(ctx context.Context, in BindInput) (out core.AccountSummary, err error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err = s.gate.Do(ctx, func() error {
		site, e := newapi.NormalizeSite(in.Site)
		if e != nil {
			return e
		}
		if len(in.Label) > 256 || in.UpstreamUserID < 0 {
			return core.Error("invalid_input")
		}
		if in.Channel == "refresh_cookie" && !in.DedicatedSessionConfirmed {
			return core.Error("invalid_input")
		}
		existing, e := s.repo.List(ctx)
		if e != nil {
			return e
		}
		old := map[string]core.Account{}
		revs := map[string]credential.Revision{}
		for _, a := range existing {
			loaded, r, e := s.repo.Load(ctx, a.ID)
			if e != nil {
				return e
			}
			old[a.ID] = loaded
			revs[a.ID] = r
		}
		if in.AccountID != "" {
			if _, ok := old[in.AccountID]; !ok {
				record, known := s.state.Read().Accounts[in.AccountID]
				if !known {
					return core.Error("credential_unreadable")
				}
				s.repo.Track(in.AccountID, record.PhysicalPath)
				missing, e := s.repo.Missing(ctx, in.AccountID)
				if e != nil {
					return e
				}
				if !missing {
					return core.Error("credential_unreadable")
				}
			}
		}
		info, e := s.client.Probe(ctx, site)
		if e != nil {
			return e
		}
		c := core.Credential{Channel: in.Channel, Generation: 1}
		var identity core.Identity
		var sessionKey string
		switch in.Channel {
		case "token":
			c.Token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.Token), "Bearer "))
			if c.Token == "" || strings.ContainsAny(c.Token, " \t\r\n") || strings.Count(c.Token, ".") == 2 {
				return core.Error("invalid_input")
			}
			c.Kind = "legacy"
			if strings.HasPrefix(c.Token, "nap_") {
				c.Kind = "pat"
			}
			c.ExpiresAtHint = in.ExpiresAtHint
		case "password", "refresh_cookie":
			var bundle core.SessionBundle
			if in.Channel == "password" {
				bundle, e = s.client.PasswordLogin(ctx, site, in.Username, in.Password)
			} else {
				refresh := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.RefreshCookie), "new_api_refresh="))
				sid, parseErr := newapi.SessionID(refresh)
				if parseErr != nil {
					return parseErr
				}
				sessionKey = site + "\n" + sid
				for _, a := range existing {
					if a.Site == site && a.Credential.SessionID == sid {
						return core.Error("session_already_managed")
					}
				}
				if known, ok := s.state.Read().KnownSessions[sessionKey]; ok && known.ExpiresAt.After(time.Now()) {
					return core.Error("session_already_managed")
				}
				if e = s.state.Update(func(v *state.Snapshot) error {
					v.KnownSessions[sessionKey] = state.KnownSession{Site: site, SessionID: sid, Status: "importing", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UTC()}
					return nil
				}); e != nil {
					return e
				}
				bundle, e = s.client.Refresh(ctx, site, refresh)
				if e != nil {
					var api *core.APIError
					definite := errors.As(e, &api) && api.HTTPStatus >= 400 && api.HTTPStatus < 500 && api.Code != ""
					saveErr := s.state.Update(func(v *state.Snapshot) error {
						if definite {
							delete(v.KnownSessions, sessionKey)
						} else {
							k := v.KnownSessions[sessionKey]
							k.Status = "unknown"
							v.KnownSessions[sessionKey] = k
						}
						return nil
					})
					if saveErr != nil {
						return saveErr
					}
					if !definite {
						return core.Error("refresh_outcome_unknown")
					}
					return e
				}
			}
			if e != nil {
				return e
			}
			identity = bundle.Identity
			c.Kind = "session"
			c.AccessToken = bundle.AccessToken
			c.RefreshToken = bundle.RefreshToken
			c.SessionID = bundle.SessionID
			c.AccessExpiresAt = bundle.AccessExpiresAt
			c.SessionExpiresAt = bundle.SessionExpiresAt
			sessionKey = site + "\n" + c.SessionID
			// Preserve a consumed/new session even when later identity or storage validation fails.
			if e = s.state.Update(func(v *state.Snapshot) error {
				v.KnownSessions[sessionKey] = state.KnownSession{Site: site, SessionID: c.SessionID, Status: "unknown", ExpiresAt: time.Unix(c.SessionExpiresAt, 0).UTC()}
				return nil
			}); e != nil {
				return e
			}
		default:
			return core.Error("invalid_input")
		}
		bearer := c.Token
		if c.Kind == "session" {
			bearer = c.AccessToken
		}
		userID := in.UpstreamUserID
		if userID == 0 && identity.ID > 0 {
			userID = identity.ID
		}
		if userID == 0 && in.AccountID != "" {
			if previous, ok := old[in.AccountID]; ok {
				userID = previous.UpstreamUserID
			} else {
				userID = s.state.Read().Accounts[in.AccountID].UpstreamUserID
			}
		}
		verified, e := s.client.Self(ctx, site, bearer, userID)
		if e != nil {
			return e
		}
		if (identity.ID != 0 && identity.ID != verified.ID) || (userID != 0 && userID != verified.ID) {
			return core.Error("identity_mismatch")
		}
		identity = verified
		id := core.AccountID(site, identity.ID)
		if in.AccountID != "" && in.AccountID != id {
			return core.Error("identity_mismatch")
		}
		status := "ready"
		if info.TurnstileCheck == nil {
			status = "unsupported_site"
		} else if *info.TurnstileCheck {
			status = "turnstile_blocked"
		}
		stats, e := s.client.CheckinStatus(ctx, site, bearer, "", identity.ID)
		statsOK := e == nil
		if e != nil {
			if core.ErrorKind(e) == "not_enabled" {
				status = "not_enabled"
			} else {
				return e
			}
		}
		if previous, ok := old[id]; ok {
			c.Generation = previous.Credential.Generation + 1
		} else if record, ok := s.state.Read().Accounts[id]; ok && record.LastGeneration >= c.Generation {
			c.Generation = record.LastGeneration + 1
		}
		a := core.Account{ID: id, Site: site, UpstreamUserID: identity.ID, Username: identity.Username, Label: strings.TrimSpace(in.Label), Credential: c}
		if a.Label == "" {
			a.Label = a.Username
		}
		if e = s.repo.Save(ctx, a, revs[id]); e != nil {
			return e
		}
		_, revision, e := s.repo.Load(ctx, id)
		if e != nil {
			return core.Error("persistence_error")
		}
		record := state.Record(a, revision.PhysicalPath, status)
		if statsOK {
			record.TotalQuota = stats.TotalQuota
			record.TotalCheckins = stats.TotalCheckins
			record.StatsSyncedAt = time.Now().UTC()
		} else {
			record.StatsStale = true
		}
		if e = s.state.Update(func(v *state.Snapshot) error {
			record.LastResult = v.Accounts[id].LastResult
			v.Accounts[id] = record
			delete(v.DeletePending, id)
			delete(v.RefreshPending, id)
			v.CancelWork(id)
			delete(v.Schedules, id)
			if sessionKey != "" {
				known := v.KnownSessions[sessionKey]
				known.Status = "bound"
				known.AccountID = id
				v.KnownSessions[sessionKey] = known
			}
			// A rebind starts with a fresh GET immediately, independent of the old retry chain.
			if _, wasBound := old[id]; (wasBound || in.AccountID != "") && status == "ready" {
				workID := "rebind-" + id + "-" + time.Now().UTC().Format("20060102T150405.000000000")
				v.WorkItems[workID] = state.WorkItem{ID: workID, AccountID: id, CreatedAt: time.Now().UTC(), Status: "queued", NextAttemptAt: time.Now().UTC(), Generation: c.Generation}
				v.Jobs[workID] = state.Job{ID: workID, Trigger: "rebind", Status: "queued", CreatedAt: time.Now().UTC(), WorkIDs: []string{workID}}
			}
			return nil
		}); e != nil {
			return e
		}
		s.broker.Forget(id)
		out = record.Summary()
		return nil
	})
	return out, err
}
func (s *Service) Recheck(ctx context.Context, id string) (out core.AccountSummary, err error) {
	err = s.broker.WithAccount(ctx, id, func(use credential.UseCredential) error {
		if s.state.Read().DeletePending[id] {
			return core.Error("delete_pending")
		}
		// Explicit recheck permits a newly validated external revision; ambiguous refresh never clears here.
		if err = func() error {
			if s.state.Read().Accounts[id].Status != "external_change" {
				return nil
			}
			a, _, e := s.repo.Load(ctx, id)
			if e != nil {
				return e
			}
			if a.Disabled {
				return core.Error("disabled")
			}
			return s.state.Update(func(v *state.Snapshot) error { r := v.Accounts[id]; r.Status = "ready"; v.Accounts[id] = r; return nil })
		}(); err != nil {
			return err
		}
		err = use(func(a core.Account, bearer string) error {
			info, e := s.client.Probe(ctx, a.Site)
			if e != nil {
				return e
			}
			if info.TurnstileCheck == nil {
				return core.Error("unsupported_site")
			}
			identity, e := s.client.Self(ctx, a.Site, bearer, a.UpstreamUserID)
			if e != nil {
				return e
			}
			if identity.ID != a.UpstreamUserID {
				return core.Error("identity_mismatch")
			}
			status := "ready"
			if *info.TurnstileCheck {
				status = "turnstile_blocked"
			}
			stats, e := s.client.CheckinStatus(ctx, a.Site, bearer, "", a.UpstreamUserID)
			statsOK := e == nil
			if e != nil {
				if core.ErrorKind(e) != "not_enabled" {
					return e
				}
				status = "not_enabled"
			}
			return s.state.Update(func(v *state.Snapshot) error {
				r := v.Accounts[id]
				if r.Status == "permission_denied" {
					r.PermissionDenied = true
				}
				if r.PermissionDenied && status == "ready" {
					status = "permission_denied"
				}
				r.Status = status
				if statsOK {
					r.TotalQuota = stats.TotalQuota
					r.TotalCheckins = stats.TotalCheckins
					r.StatsSyncedAt = time.Now().UTC()
				}
				r.StatsStale = !statsOK
				r.LastGeneration = a.Credential.Generation
				v.Accounts[id] = r
				out = r.Summary()
				return nil
			})
		})
		if err != nil {
			if e := s.state.Update(func(v *state.Snapshot) error {
				r, ok := v.Accounts[id]
				if ok {
					if r.Status == "permission_denied" || core.ErrorKind(err) == "permission_denied" {
						r.PermissionDenied = true
					}
					r.Status = core.ErrorKind(err)
					v.Accounts[id] = r
				}
				return nil
			}); e != nil {
				err = e
			}
		}
		return err
	})
	return out, err
}
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.gate.Do(ctx, func() error {
		if _, ok := s.state.Read().Accounts[id]; !ok {
			return core.Error("invalid_input")
		}
		if e := s.state.Update(func(v *state.Snapshot) error {
			v.DeletePending[id] = true
			delete(v.RefreshPending, id)
			v.CancelWork(id)
			delete(v.Schedules, id)
			a := v.Accounts[id]
			a.Status = "delete_pending"
			v.Accounts[id] = a
			return nil
		}); e != nil {
			return e
		}
		s.broker.Forget(id)
		deleteErr := s.repo.Delete(ctx, id)
		if e := s.state.Update(func(v *state.Snapshot) error {
			a := v.Accounts[id]
			a.Status = "deleted"
			if deleteErr != nil {
				a.Status = "delete_failed"
			}
			v.Accounts[id] = a
			return nil
		}); e != nil {
			return e
		}
		return deleteErr
	})
}

// Reconcile discovers credentials without erasing histories during an empty host enumeration.
func (s *Service) Reconcile(ctx context.Context) error {
	return s.gate.Do(ctx, func() error {
		snapshot := s.state.Read()
		for id, a := range snapshot.Accounts {
			s.repo.Track(id, a.PhysicalPath)
		}
		accounts, e := s.repo.List(ctx)
		if e != nil {
			return e
		}
		return s.state.Update(func(v *state.Snapshot) error {
			for id, record := range v.Accounts {
				if v.DeletePending[id] {
					continue
				}
				a, _, err := s.repo.Load(ctx, id)
				if core.ErrorKind(err) == "host_unavailable" {
					return err
				}
				if err != nil {
					record.Status = "credential_unreadable"
					v.Accounts[id] = record
					continue
				}
				if a.Disabled {
					record.Status = "disabled"
					v.Accounts[id] = record
					continue
				}
				if a.Credential.Generation != record.LastGeneration {
					pending, refreshing := v.RefreshPending[id]
					// Recovery owns a generation already saved by our pending refresh.
					if !refreshing || a.Credential.Generation <= pending.OldGeneration {
						record.Status = "external_change"
						v.Accounts[id] = record
					}
				}
			}
			for _, a := range accounts {
				if v.DeletePending[a.ID] {
					continue
				}
				if _, ok := v.Accounts[a.ID]; ok {
					continue
				}
				_, rev, e := s.repo.Load(ctx, a.ID)
				if e != nil {
					continue
				}
				status := "ready"
				if a.Disabled {
					status = "disabled"
				}
				v.Accounts[a.ID] = state.Record(a, rev.PhysicalPath, status)
			}
			return nil
		})
	})
}
