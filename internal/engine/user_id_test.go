package engine

import (
	"context"
	"net/http"
	"testing"
)

func TestCheckinSendsPersistedUserID(t *testing.T) {
	f := fixture(t)
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/checkin" && r.Header.Get("New-Api-User") != "1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"Unauthorized, New-Api-User header not provided"}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	r, err := f.runner.Attempt(context.Background(), f.id)
	if err != nil || r.Status != "success" || r.QuotaAwarded != 7 || f.posts.Load() != 1 {
		t.Fatalf("check-in failed with persisted identity: result=%+v err=%v posts=%d", r, err, f.posts.Load())
	}
	if f.state.Read().Accounts[f.id].StatsStale {
		t.Fatal("post-check-in statistics did not include user ID")
	}
	r, err = f.runner.Attempt(context.Background(), f.id)
	if err != nil || r.Status != "already" || f.posts.Load() != 1 {
		t.Fatalf("duplicate check-in should be skipped: result=%+v err=%v", r, err)
	}
}
