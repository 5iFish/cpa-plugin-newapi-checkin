package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"time"
	_ "time/tzdata"
)

func Defaults() Settings { return Settings{Enabled: true, CheckinAt: "09:00", JitterMinutes: 20} }
func Parse(b []byte) (Settings, error) {
	s := Defaults()
	if len(b) > 0 {
		if e := yaml.Unmarshal(b, &s); e != nil {
			return s, fmt.Errorf("invalid plugin settings")
		}
	}
	return s, Validate(s)
}
func Merge(base Settings, o Overrides) (Settings, error) {
	if o.CheckinAt != nil {
		base.CheckinAt = *o.CheckinAt
	}
	if o.JitterMinutes != nil {
		base.JitterMinutes = *o.JitterMinutes
	}
	if o.Timezone != nil {
		base.Timezone = *o.Timezone
	}
	return base, Validate(base)
}
func Location(s Settings) (*time.Location, error) {
	if s.Timezone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(s.Timezone)
}
func Validate(s Settings) error {
	if len(s.CheckinAt) != 5 {
		return fmt.Errorf("checkin_at must be HH:MM")
	}
	if _, e := time.Parse("15:04", s.CheckinAt); e != nil {
		return fmt.Errorf("checkin_at must be HH:MM")
	}
	if s.JitterMinutes < 0 || s.JitterMinutes > 120 {
		return fmt.Errorf("jitter_minutes must be 0–120")
	}
	if _, e := Location(s); e != nil {
		return fmt.Errorf("invalid IANA timezone")
	}
	return nil
}
