package core

import (
	"encoding/json"
	"testing"
)

func TestEffectiveCheckinModeDefaultsToAPI(t *testing.T) {
	if m := (Account{}).EffectiveCheckinMode(); m != CheckinModeAPI {
		t.Fatal(m)
	}
	if m := (Account{CheckinMode: CheckinModeLogin}).EffectiveCheckinMode(); m != CheckinModeLogin {
		t.Fatal(m)
	}
	// Anything unknown degrades conservatively to the standard API mode.
	if m := (Account{CheckinMode: "weird"}).EffectiveCheckinMode(); m != CheckinModeAPI {
		t.Fatal(m)
	}
}
func TestValidCheckinMode(t *testing.T) {
	for _, ok := range []string{"", CheckinModeAPI, CheckinModeLogin} {
		if !ValidCheckinMode(ok) {
			t.Fatal(ok)
		}
	}
	for _, bad := range []string{"API", "checkin", "oauth", " "} {
		if ValidCheckinMode(bad) {
			t.Fatal(bad)
		}
	}
}

func TestIdentityTracksRewardFieldPresence(t *testing.T) {
	var present Identity
	if e := json.Unmarshal([]byte(`{"id":7,"username":"u","quota":0,"used_quota":0,"last_login_time":0}`), &present); e != nil {
		t.Fatal(e)
	}
	if !present.HasRewardEvidence() || !present.QuotaPresent || !present.UsedQuotaPresent || !present.LastLoginPresent {
		t.Fatal("zero is a valid value and must still count as present", present)
	}
	var absent Identity
	if e := json.Unmarshal([]byte(`{"id":7,"username":"u"}`), &absent); e != nil {
		t.Fatal(e)
	}
	if absent.HasRewardEvidence() || absent.QuotaPresent || absent.UsedQuotaPresent || absent.LastLoginPresent {
		t.Fatal("missing fields must stay distinguishable from zero", absent)
	}
}

func TestIdentityUsesDailyAllowanceAsRewardEvidence(t *testing.T) {
	var i Identity
	// setting is a JSON string nested inside the response, as returned by
	// NewAPI forks that credit a fixed quota on the first visit of each day.
	raw := `{"id":2579,"username":"5iFish","quota":31779621,"used_quota":18220379,"setting":"{\"allowance_date\":\"2026-11-16\",\"sidebar_modules\":\"{}\"}"}`
	if e := json.Unmarshal([]byte(raw), &i); e != nil {
		t.Fatal(e)
	}
	if !i.HasRewardEvidence() || !i.AllowancePresent || i.AllowanceDate != "2026-11-16" {
		t.Fatal("daily allowance must count as reward evidence", i)
	}
	if !i.LoginEvidenceOn("2026-11-16", 0) {
		t.Fatal("allowance dated today is a same-day signal")
	}
	if i.LoginEvidenceOn("2026-11-17", 0) {
		t.Fatal("yesterday's allowance must not signal today")
	}
	// Older forks store setting as an object instead of a JSON string.
	var obj Identity
	if e := json.Unmarshal([]byte(`{"id":2,"quota":1,"used_quota":0,"setting":{"allowance_date":"2026-11-16"}}`), &obj); e != nil {
		t.Fatal(e)
	}
	if !obj.AllowancePresent || obj.AllowanceDate != "2026-11-16" || !obj.LoginEvidenceOn("2026-11-16", 0) {
		t.Fatal("object-form setting must expose the allowance", obj)
	}
}

func TestIdentityIgnoresMalformedSetting(t *testing.T) {
	var i Identity
	if e := json.Unmarshal([]byte(`{"id":1,"quota":1,"used_quota":0,"setting":"not-json"}`), &i); e != nil {
		t.Fatal(e)
	}
	if i.AllowancePresent || i.HasRewardEvidence() {
		t.Fatal("malformed setting must not invent reward evidence", i)
	}
}
