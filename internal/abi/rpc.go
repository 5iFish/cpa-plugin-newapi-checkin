package abi

import (
	"context"
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const Name = "newapi-checkin"
const Prefix = "/plugins/" + Name

type Dispatcher interface {
	Handle(context.Context, string, json.RawMessage) (json.RawMessage, error)
	Close(context.Context) error
}
type RegisterResult struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  map[string]bool    `json:"capabilities"`
}

func Registration() RegisterResult {
	return RegisterResult{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{Name: Name, Version: "0.1.1", Author: "5ifish", GitHubRepository: "https://github.com/5ifish/cpa-plugin-newapi-checkin", ConfigFields: []pluginapi.ConfigField{
			{Name: "checkin-at", Type: pluginapi.ConfigFieldTypeString, Description: "Daily check-in time (09:00)"},
			{Name: "jitter-minutes", Type: pluginapi.ConfigFieldTypeInteger, Description: "Daily jitter in minutes (0–120, default 20)"},
			{Name: "timezone", Type: pluginapi.ConfigFieldTypeString, Description: "IANA time zone; empty means host local"},
		}}, Capabilities: map[string]bool{"management_api": true},
	}
}
func DecodeConfig(b []byte) ([]byte, error) {
	var r struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	err := json.Unmarshal(b, &r)
	return r.ConfigYAML, err
}

type Routes struct {
	Routes    []pluginapi.ManagementRoute `json:"routes"`
	Resources []pluginapi.ResourceRoute   `json:"resources"`
}

func ManagementRegistration() Routes {
	return Routes{
		Routes: []pluginapi.ManagementRoute{
			{Method: "GET", Path: Prefix + "/status"}, {Method: "POST", Path: Prefix + "/accounts"}, {Method: "DELETE", Path: Prefix + "/accounts"},
			{Method: "POST", Path: Prefix + "/checkin"}, {Method: "POST", Path: Prefix + "/recheck"}, {Method: "POST", Path: Prefix + "/settings"},
		}, Resources: []pluginapi.ResourceRoute{{Path: "/console", Menu: "NewAPI 签到", Description: "站点签到与凭证管理"}, {Path: "/console.css"}, {Path: "/console.js"}},
	}
}
func Result(v any) (json.RawMessage, error) { return json.Marshal(v) }
func Envelope(result json.RawMessage, err error) []byte {
	e := pluginabi.Envelope{OK: err == nil, Result: result}
	if err != nil {
		e.Result = nil
		e.Error = &pluginabi.Error{Code: "plugin_error", Message: "Plugin operation failed"}
	}
	b, _ := json.Marshal(e)
	return b
}
