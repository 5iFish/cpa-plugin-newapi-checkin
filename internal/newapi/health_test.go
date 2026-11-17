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

func TestHealthLogsUsesAccountAndHalfOpenWindow(t *testing.T) {
	for _, kind := range []int{2, 5} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/log/self" || r.Header.Get("Authorization") != "Bearer nap_health-test" || r.Header.Get("New-Api-User") != "7" {
					t.Errorf("incorrect account request: %s %s", r.Method, r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("type") != fmt.Sprint(kind) || q.Get("p") != "2" || q.Get("page_size") != "100" || q.Get("start_timestamp") != "1000" || q.Get("end_timestamp") != "1899" || len(q) != 5 {
					t.Errorf("incorrect health query: %v", q)
				}
				_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[{"type":%d,"created_at":1000,"content":"private-error-body","token_name":"secret-name","ip":"private-ip","other":"secret-metadata"},{"type":%d,"created_at":1899}],"total":102}}`, kind, kind)
			}))
			t.Cleanup(server.Close)
			got, err := NewClient(server.Client()).HealthLogs(context.Background(), server.URL, "nap_health-test", 7, kind, 2, 1000, 1900)
			if err != nil || len(got.Timestamps) != 2 || got.Timestamps[0] != 1000 || got.Timestamps[1] != 1899 || !got.TotalKnown || got.Total != 102 {
				t.Fatalf("health page: %+v, error: %v", got, err)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "private") {
				t.Fatalf("request details retained: %s", encoded)
			}
		})
	}
}

func TestHealthLogsEmptyPageIsNotUnsupported(t *testing.T) {
	for _, data := range []string{`{"items":[],"total":0}`, `{"items":[]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"success":true,"data":%s}`, data)
		}))
		t.Cleanup(server.Close)
		got, err := NewClient(server.Client()).HealthLogs(context.Background(), server.URL, "tok", 1, 5, 1, 1000, 1900)
		if err != nil || len(got.Timestamps) != 0 || got.TotalKnown != strings.Contains(data, "total") {
			t.Fatalf("valid empty page rejected: %+v %v", got, err)
		}
	}
}

func TestHealthLogsRejectsMissingFieldsAndIgnoredFilters(t *testing.T) {
	for name, data := range map[string]string{
		"missing items":    `{}`,
		"null items":       `{"items":null}`,
		"non-array items":  `{"items":{}}`,
		"missing type":     `{"items":[{"created_at":1000}]}`,
		"wrong type":       `{"items":[{"type":2,"created_at":1000}]}`,
		"missing time":     `{"items":[{"type":5}]}`,
		"string time":      `{"items":[{"type":5,"created_at":"1000"}]}`,
		"fractional time":  `{"items":[{"type":5,"created_at":1000.5}]}`,
		"zero time":        `{"items":[{"type":5,"created_at":0}]}`,
		"before window":    `{"items":[{"type":5,"created_at":999}]}`,
		"exclusive end":    `{"items":[{"type":5,"created_at":1900}]}`,
		"negative total":   `{"items":[],"total":-1}`,
		"string total":     `{"items":[],"total":"1"}`,
		"total smaller":    `{"items":[{"type":5,"created_at":1000}],"total":0}`,
		"empty first page": `{"items":[],"total":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"success":true,"data":%s}`, data)
			}))
			t.Cleanup(server.Close)
			_, err := NewClient(server.Client()).HealthLogs(context.Background(), server.URL, "tok", 1, 5, 1, 1000, 1900)
			if core.ErrorKind(err) != "unsupported_site" {
				t.Fatalf("invalid log data accepted: %s, error: %v", data, err)
			}
		})
	}
}

func TestHealthLogsValidatesInputsBeforeRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(server.Close)
	client := NewClient(server.Client())
	for _, tc := range []struct {
		kind, page int
		start, end int64
	}{
		{0, 1, 1000, 1900}, {3, 1, 1000, 1900}, {2, 0, 1000, 1900}, {2, 1, 0, 1900}, {5, 1, 1900, 1000}, {5, 1, 1000, 1000},
	} {
		if _, err := client.HealthLogs(context.Background(), server.URL, "tok", 1, tc.kind, tc.page, tc.start, tc.end); core.ErrorKind(err) != "invalid_input" {
			t.Fatalf("invalid input accepted: %+v %v", tc, err)
		}
	}
	if hits != 0 {
		t.Fatalf("invalid input sent %d requests", hits)
	}
}

func TestHealthLogsPropagatesSafeBusinessErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED","message":"private-error-token"}`))
	}))
	t.Cleanup(server.Close)
	_, err := NewClient(server.Client()).HealthLogs(context.Background(), server.URL, "tok", 1, 5, 1, 1000, 1900)
	if core.ErrorKind(err) != "permission_denied" || strings.Contains(err.Error(), "private-error-token") {
		t.Fatalf("unsafe or incorrect error: %v", err)
	}
}
