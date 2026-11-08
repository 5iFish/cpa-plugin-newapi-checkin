package config

import (
	"encoding/json"
	"testing"
)

func TestSettingsDefaultsAndNullReset(t *testing.T) {
	s, e := Parse(nil)
	if e != nil || !s.Enabled || s.CheckinAt != "09:00" || s.JitterMinutes != 20 || s.Timezone != "" {
		t.Fatal(s, e)
	}
	var o Overrides
	_ = json.Unmarshal([]byte(`{"jitter_minutes":0,"timezone":"UTC","checkin_at":"12:30"}`), &o)
	s, e = Merge(s, o)
	if e != nil || s.JitterMinutes != 0 || s.CheckinAt != "12:30" {
		t.Fatal(s, e)
	}
	_ = json.Unmarshal([]byte(`{"jitter_minutes":null,"timezone":null,"checkin_at":null}`), &o)
	s, _ = Merge(Defaults(), o)
	if s.JitterMinutes != 20 || s.CheckinAt != "09:00" {
		t.Fatal(s)
	}
}
func TestInvalidSettingsKeepOldSchedule(t *testing.T) {
	for _, yaml := range []string{"checkin-at: '24:00'", "jitter-minutes: -1", "jitter-minutes: 121", "timezone: Mars/Unknown", "enabled: nope"} {
		if _, e := Parse([]byte(yaml)); e == nil {
			t.Fatal(yaml)
		}
	}
}
