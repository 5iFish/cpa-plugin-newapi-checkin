package abi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistrationManagementOnly(t *testing.T) {
	r := Registration()
	b, _ := json.Marshal(r)
	var data struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Capabilities) != 1 || !data.Capabilities["management_api"] {
		t.Fatalf("unexpected capabilities: %s", b)
	}
}
func TestLifecycleConfigDecodeOnce(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"config_yaml": []byte("enabled: true\n")})
	got, err := DecodeConfig(input)
	if err != nil || string(got) != "enabled: true\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err = DecodeConfig([]byte(`{"config_yaml":"%%%"}`)); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}
func TestManagementPathPrefix(t *testing.T) {
	r := ManagementRegistration()
	if len(r.Routes) != 6 || len(r.Resources) != 3 {
		t.Fatalf("routes: %+v", r)
	}
	for _, v := range r.Routes {
		if !strings.HasPrefix(v.Path, "/plugins/newapi-checkin/") || v.Menu != "" {
			t.Fatal(v.Path)
		}
	}
	for _, v := range r.Resources {
		if (v.Path == "/console") != (v.Menu != "") {
			t.Fatal(v.Path)
		}
	}
}
