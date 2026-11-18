package newapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

const (
	modelsMaxCount     = 2000
	modelsMaxNameRunes = 200
)

// UserModels returns the model names the account's user group may call
// (GET /api/user/models). Only names are kept; a name that is blank or a list
// that is malformed or oversized is not model availability data.
func (c *Client) UserModels(ctx context.Context, site, bearer string, userID int64) ([]string, error) {
	if userID <= 0 {
		return nil, core.Error("invalid_input")
	}
	raw, _, err := c.request(ctx, site, "/api/user/models", "GET", bearer, nil, userHeaders(userID))
	if err != nil {
		return nil, err
	}
	var names []string
	if json.Unmarshal(raw, &names) != nil || len(names) > modelsMaxCount {
		return nil, core.Error("unsupported_site")
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" || len([]rune(name)) > modelsMaxNameRunes {
			return nil, core.Error("unsupported_site")
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}
