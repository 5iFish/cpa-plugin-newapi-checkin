package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

func TestUserModelsUsesAccountCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/user/models" || r.Header.Get("Authorization") != "Bearer nap_models-test" || r.Header.Get("New-Api-User") != "9" || len(r.URL.Query()) != 0 {
			t.Errorf("incorrect account request: %s %s %v", r.Method, r.URL.Path, r.URL.Query())
		}
		_, _ = fmt.Fprint(w, `{"success":true,"message":"","data":["gpt-4o","claude-3-7","gpt-4o","deepseek-chat"]}`)
	}))
	t.Cleanup(server.Close)
	got, err := NewClient(server.Client()).UserModels(context.Background(), server.URL, "nap_models-test", 9)
	if err != nil || strings.Join(got, ",") != "gpt-4o,claude-3-7,deepseek-chat" {
		t.Fatalf("user models: %v, error: %v", got, err)
	}
	encoded, _ := json.Marshal(got)
	if !strings.Contains(string(encoded), "gpt-4o") {
		t.Fatalf("model names lost: %s", encoded)
	}
}

func TestUserModelsEmptyListIsValid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"success":true,"data":[]}`)
	}))
	t.Cleanup(server.Close)
	got, err := NewClient(server.Client()).UserModels(context.Background(), server.URL, "tok", 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("valid empty model list rejected: %v %v", got, err)
	}
}

func TestUserModelsRejectsMalformedData(t *testing.T) {
	long := strings.Repeat("m", 201)
	many := make([]string, 2001)
	for i := range many {
		many[i] = "gpt"
	}
	for name, data := range map[string]any{
		"object data":     `{"id":"gpt-4o"}`,
		"object entry":    `[{"id":"gpt-4o"}]`,
		"number entry":    `[1]`,
		"null entry":      `[null]`,
		"empty name":      `[""]`,
		"blank name":      `["   "]`,
		"too long":        `["` + long + `"]`,
		"too many models": many,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch v := data.(type) {
				case string:
					_, _ = fmt.Fprintf(w, `{"success":true,"data":%s}`, v)
				case []string:
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": v})
				}
			}))
			t.Cleanup(server.Close)
			if _, err := NewClient(server.Client()).UserModels(context.Background(), server.URL, "tok", 1); core.ErrorKind(err) != "unsupported_site" {
				t.Fatalf("invalid model data accepted: %v, error: %v", data, err)
			}
		})
	}
}

func TestUserModelsValidatesInputBeforeRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(server.Close)
	if _, err := NewClient(server.Client()).UserModels(context.Background(), server.URL, "tok", 0); core.ErrorKind(err) != "invalid_input" {
		t.Fatalf("missing user id accepted: %v", err)
	}
	if hits != 0 {
		t.Fatalf("invalid input sent %d requests", hits)
	}
}
