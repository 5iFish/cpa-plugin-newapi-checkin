package account

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
)

func TestModelsCachedWithinHealthRefresh(t *testing.T) {
	var hits atomic.Int32
	var logHits atomic.Int32
	svc, st, id := healthFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			logHits.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("New-Api-User") != "7" {
				t.Errorf("models used the wrong identity: %s", r.Header.Get("New-Api-User"))
			}
			hits.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":["gpt-4o","deepseek-chat","gpt-4o"],"secret_token":"nap_models-secret"}`))
		})
	started, err := svc.RefreshHealth(context.Background(), false)
	if err != nil || !started {
		t.Fatal(started, err)
	}
	waitHealth(t, svc)
	models := svc.ModelsStatus().Accounts[id]
	if !models.Complete || models.Stale || models.Error != "" || strings.Join(models.Models, ",") != "gpt-4o,deepseek-chat" {
		t.Fatalf("bad models summary: %+v", models)
	}
	if models.SyncedAt.IsZero() || models.CheckedAt.IsZero() || hits.Load() != 1 {
		t.Fatal(models.SyncedAt, models.CheckedAt, hits.Load())
	}
	encoded, _ := json.Marshal(models)
	if strings.Contains(string(encoded), "nap_models-secret") || strings.Contains(string(encoded), "secret_token") {
		t.Fatalf("secret in models cache: %s", encoded)
	}
	if cached := st.Read().Models[id]; !cached.Complete || len(cached.Models) != 2 {
		t.Fatalf("models cache missing: %+v", cached)
	}
	if started, err = svc.RefreshHealth(context.Background(), false); err != nil || started || hits.Load() != 1 || logHits.Load() != 2 {
		t.Fatalf("fresh caches refetched: started=%v models=%d logs=%d err=%v", started, hits.Load(), logHits.Load(), err)
	}
}

func TestModelsRefreshedWhenStaleEvenIfHealthFresh(t *testing.T) {
	var modelHits, logHits atomic.Int32
	svc, st, id := healthFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			logHits.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			modelHits.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":["claude-3-7"]}`))
		})
	_, _ = svc.RefreshHealth(context.Background(), true)
	waitHealth(t, svc)
	old := time.Now().Add(-13 * time.Hour)
	if err := st.Update(func(v *state.Snapshot) error {
		cached := v.Models[id]
		cached.SyncedAt, cached.CheckedAt = old, old
		v.Models[id] = cached
		fresh := v.Health[id]
		fresh.CheckedAt = time.Now()
		v.Health[id] = fresh
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if models := svc.ModelsStatus().Accounts[id]; !models.Stale {
		t.Fatalf("old models list not marked stale: %+v", models)
	}
	started, err := svc.RefreshHealth(context.Background(), false)
	if err != nil || !started {
		t.Fatal("stale models alone did not start a round", started, err)
	}
	waitHealth(t, svc)
	if modelHits.Load() != 2 || logHits.Load() != 2 {
		t.Fatalf("wrong fetch mix: models=%d logs=%d", modelHits.Load(), logHits.Load())
	}
	if updated := svc.ModelsStatus().Accounts[id]; updated.Stale || strings.Join(updated.Models, ",") != "claude-3-7" {
		t.Fatalf("models not refreshed: %+v", updated)
	}
}

func TestModelsFailureRetainsPreviousList(t *testing.T) {
	var fail atomic.Bool
	var hits atomic.Int32
	svc, _, id := healthFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if fail.Load() {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"success":false,"message":"private-upstream-body"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":["gpt-4o","claude-3-7"]}`))
		})
	_, _ = svc.RefreshHealth(context.Background(), true)
	waitHealth(t, svc)
	fail.Store(true)
	_, _ = svc.RefreshHealth(context.Background(), true)
	waitHealth(t, svc)
	models := svc.ModelsStatus().Accounts[id]
	if !models.Stale || !models.Complete || strings.Join(models.Models, ",") != "gpt-4o,claude-3-7" || models.Error != "upstream_error" || models.CheckedAt.IsZero() {
		t.Fatalf("previous model list lost on failure: %+v", models)
	}
	if hits.Load() != 2 {
		t.Fatalf("unexpected model fetch count: %d", hits.Load())
	}
}

func TestModelsUnsupportedEndpointIsRecordedNotComplete(t *testing.T) {
	svc, _, id := healthFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	_, _ = svc.RefreshHealth(context.Background(), false)
	waitHealth(t, svc)
	models := svc.ModelsStatus().Accounts[id]
	if models.Complete || models.Error != "unsupported_site" || len(models.Models) != 0 {
		t.Fatalf("unsupported endpoint fabricated a list: %+v", models)
	}
}

func TestModelsWorkerDisabledWithoutStart(t *testing.T) {
	svc, _, _ := healthFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		})
	if err := svc.ShutdownHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshHealth(context.Background(), true); core.ErrorKind(err) != "disabled" {
		t.Fatalf("shutdown allowed a models worker: %v", err)
	}
	if got := svc.ModelsStatus(); got.Refreshing || len(got.Accounts) != 0 || got.Accounts == nil {
		t.Fatalf("models status after shutdown: %+v", got)
	}
}
