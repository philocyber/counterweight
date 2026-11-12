package db

import "testing"

func TestSubscriptionProfileRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	p := &LLMProfile{Name: "subscription-roundtrip", Format: "openai-responses", Model: "account-model", SubscriptionID: "opaque-account-id", CyberAccessProgram: "daybreakBlue", Streaming: true}
	id, err := d.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
	check := func() {
		t.Helper()
		got, err := d.ProfileByID(id)
		if err != nil || got == nil || got.SubscriptionID != p.SubscriptionID || got.CyberAccessProgram != p.CyberAccessProgram || got.APIKey != "" {
			t.Fatalf("subscription not preserved: %+v %v", got, err)
		}
		listed, err := d.ListProfiles()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, profile := range listed {
			if profile.ID == id {
				found = profile.SubscriptionID == p.SubscriptionID && profile.APIKeyHint == ""
			}
		}
		if !found {
			t.Fatal("subscription not listed")
		}
		pool, err := d.PoolProfiles()
		if err != nil {
			t.Fatal(err)
		}
		found = false
		for _, profile := range pool {
			if profile.ID == id {
				found = profile.SubscriptionID == p.SubscriptionID
			}
		}
		if !found {
			t.Fatal("subscription missing from pool")
		}
	}
	check()
	p.ID = id
	p.Name = "subscription-edited"
	if _, err = d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	check()
	// Changing to API billing replaces the subscription reference explicitly.
	p.SubscriptionID = ""
	p.APIKey = "fixture-key"
	if _, err = d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	got, _ := d.ProfileByID(id)
	if got.SubscriptionID != "" || got.APIKey != "fixture-key" {
		t.Fatal("switch to API key failed")
	}
	// Changing back must remove the old API key and its display hint.
	p.SubscriptionID = "opaque-account-id"
	if _, err = d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	check()
}
