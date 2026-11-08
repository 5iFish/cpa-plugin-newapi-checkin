package console

import (
	"embed"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"strings"
)

//go:embed web/console.html web/console.css web/console.js
var assets embed.FS

func Resource(path string) (pluginapi.ManagementResponse, bool) {
	prefix := "/v0/resource/plugins/newapi-checkin/"
	if !strings.HasPrefix(path, prefix) {
		return pluginapi.ManagementResponse{}, false
	}
	name := strings.TrimPrefix(path, prefix)
	kind := ""
	switch name {
	case "console":
		name = "console.html"
		kind = "text/html; charset=utf-8"
	case "console.css":
		kind = "text/css; charset=utf-8"
	case "console.js":
		kind = "text/javascript; charset=utf-8"
	default:
		return pluginapi.ManagementResponse{}, false
	}
	b, e := assets.ReadFile("web/" + name)
	if e != nil {
		return pluginapi.ManagementResponse{}, false
	}
	return pluginapi.ManagementResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {kind}, "Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"}, "Content-Security-Policy": {"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"}}, Body: b}, true
}
