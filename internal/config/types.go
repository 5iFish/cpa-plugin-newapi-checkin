package config

type Settings struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	CheckinAt     string `json:"checkin_at" yaml:"checkin-at"`
	JitterMinutes int    `json:"jitter_minutes" yaml:"jitter-minutes"`
	Timezone      string `json:"timezone" yaml:"timezone"`
}
type Overrides struct {
	CheckinAt     *string `json:"checkin_at"`
	JitterMinutes *int    `json:"jitter_minutes"`
	Timezone      *string `json:"timezone"`
}
