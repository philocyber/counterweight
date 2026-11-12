package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestMaskedValueHidesBodyButKeepsTailHint(t *testing.T) {
	const secret = "https://oapi.dingtalk.com/robot/send?access_token=abcdef123456"
	got := MaskedValue(secret)
	if strings.Contains(got, "abcdef123456") {
		t.Fatalf("Mask leaked the entire credential: %q", got)
	}
	if strings.Contains(got, "oapi.dingtalk.com") {
		t.Fatalf("Mask must not reveal the URL body: %q", got)
	}
	// Keep the last six characters so operators can recognize the bot.
	if !strings.HasSuffix(got, "123456") {
		t.Fatalf("Preserve the last six characters as an identifier: %q", got)
	}
	if !IsMasked(got) {
		t.Fatalf("IsMasked must recognize the mask: %q", got)
	}
}

func TestMaskedValueShortSecretGivesNoHint(t *testing.T) {
	// Exposing six characters of a short secret could reveal it entirely.
	for _, s := range []string{"abc", "abcdef", ""} {
		got := MaskedValue(s)
		if got != MaskedPrefix {
			t.Fatalf("Credential of length %d must not expose a suffix, got %q", len(s), got)
		}
		if s != "" && strings.Contains(got, s) {
			t.Fatalf("Mask contains the original value: %q", got)
		}
	}
}

func TestMaskConfigMasksOnlySecrets(t *testing.T) {
	cfg := map[string]any{
		"webhook": "https://example.com/hook?token=SECRETVALUE",
		"secret":  "SECtest123456",
		"port":    float64(587),
		"host":    "smtp.example.com",
	}
	masked := MaskConfig(KindDingTalk, cfg)
	for _, k := range []string{"webhook", "secret"} {
		s, _ := masked[k].(string)
		if !IsMasked(s) {
			t.Errorf("%s must be masked, got %q", k, s)
		}
	}
	// Preserve non-secret fields for UI display.
	if masked["port"] != float64(587) {
		t.Errorf("Non-secret port field must remain unchanged: %v", masked["port"])
	}
}

func TestMaskConfigUnknownKindReturnsEmpty(t *testing.T) {
	// Unknown kinds must return empty configuration instead of exposing potential credentials.
	got := MaskConfig("nope", map[string]any{"webhook": "https://x/y?token=LEAK"})
	if len(got) != 0 {
		t.Fatalf("Unknown kind must return empty configuration, got %v", got)
	}
}

func TestMaskConfigDoesNotMutateInput(t *testing.T) {
	// Masking is presentation-only and must not mutate stored secrets.
	cfg := map[string]any{"webhook": "https://example.com/hook", "secret": "SECtest123456"}
	_ = MaskConfig(KindDingTalk, cfg)
	if IsMasked(cfg["secret"].(string)) {
		t.Fatal("MaskConfig mutated its input, replacing real credentials with masks")
	}
}

func TestMergeConfigKeepsStoredOnMaskedIncoming(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET", "method": "POST"}
	// Only method changed; the browser submits masked credentials and the new method.
	incoming := map[string]any{
		"webhook": MaskedValue("https://real/hook"),
		"secret":  MaskedValue("REALSECRET"),
		"method":  "PUT",
	}
	got := MergeConfig(stored, incoming)
	if got["webhook"] != "https://real/hook" || got["secret"] != "REALSECRET" {
		t.Fatalf("Masked field must preserve stored value, got %v", got)
	}
	if got["method"] != "PUT" {
		t.Fatalf("Changed field must take effect, got %v", got["method"])
	}
}

func TestMergeConfigEmptyStringClears(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET"}
	got := MergeConfig(stored, map[string]any{"secret": ""})
	if _, ok := got["secret"]; ok {
		t.Fatalf("Empty string must clear the field, got %v", got)
	}
	// Omitted fields remain under partial-update semantics.
	if got["webhook"] != "https://real/hook" {
		t.Fatalf("Omitted field must remain, got %v", got)
	}
}

func TestMergeConfigKeepsUnmentionedStoredKeys(t *testing.T) {
	stored := map[string]any{"host": "smtp.example.com", "port": float64(587), "password": "pw"}
	got := MergeConfig(stored, map[string]any{"port": float64(465)})
	if got["host"] != "smtp.example.com" || got["password"] != "pw" {
		t.Fatalf("Omitted field must remain, got %v", got)
	}
	if got["port"] != float64(465) {
		t.Fatalf("Supplied field must update, got %v", got["port"])
	}
}

// TestPrepareConfigUpdateBlocksDestinationSwap enforces the key security invariant: a new destination
// must not inherit old credentials. Use actual attack-shaped input that changes only the destination,
// not merely valid defensive-path input that could pass even without protection.
func TestPrepareConfigUpdateBlocksDestinationSwap(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
		// wantMissing lists credential keys expected in the error.
		wantMissing string
	}{
		{
			name: "webhook destination swap retaining Authorization",
			kind: KindWebhook,
			stored: map[string]any{
				"url":     "https://legit.example.com/hook",
				"headers": map[string]any{"Authorization": "Bearer REAL-TOKEN"},
			},
			incoming:    map[string]any{"url": "https://attacker.tld/c"},
			wantMissing: "headers",
		},
		{
			name:        "Telegram base_url swap exfiltrating bot token",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld"},
			wantMissing: "bot_token",
		},
		{
			name:        "SMTP host swap exfiltrating password",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"host": "smtp.attacker.tld"},
			wantMissing: "password",
		},
		{
			name:        "disabling email TLS requires explicit password choice",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "tls": false, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"tls": true},
			wantMissing: "password",
		},
		{
			// Masks mean reuse of old credentials and must also fail after a destination change.
			name:        "masked credentials with new destination",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld", "bot_token": MaskedValue("123456:REAL")},
			wantMissing: "bot_token",
		},
		{
			name:        "DingTalk webhook swap retaining signing secret",
			kind:        KindDingTalk,
			stored:      map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=OLD", "secret": "REALSEC"},
			incoming:    map[string]any{"webhook": "https://attacker.tld/hook"},
			wantMissing: "secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err == nil {
				t.Fatalf("Destination change without explicit credentials must fail; got configuration %v", merged)
			}
			var target *ErrDestinationChangedWithoutCredentials
			if !errors.As(err, &target) {
				t.Fatalf("Expected dedicated actionable error type, got %T: %v", err, err)
			}
			found := false
			for _, m := range target.Missing {
				if m == tc.wantMissing {
					found = true
				}
			}
			if !found {
				t.Fatalf("Missing credential key %q must be identified, got %v", tc.wantMissing, target.Missing)
			}
			// Explain how the operator can repair the request.
			if !strings.Contains(err.Error(), tc.wantMissing) {
				t.Errorf("Error must mention %q: %v", tc.wantMissing, err)
			}
		})
	}
}

// TestPrepareConfigUpdateAllowsLegitimateEdits ensures ordinary edits remain usable; overly disruptive
// protection encourages bypasses or removal.
func TestPrepareConfigUpdateAllowsLegitimateEdits(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
	}{
		{
			name:     "rename only with unchanged configuration",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "headers": map[string]any{"Authorization": "Bearer REAL"}},
			incoming: map[string]any{"url": MaskedValue("https://legit.example.com/hook")},
		},
		{
			name:     "method change preserves destination and credentials",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "method": "POST"},
			incoming: map[string]any{"method": "PUT"},
		},
		{
			name:     "new destination with new credentials",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": map[string]any{"Authorization": "Bearer NEW"}},
		},
		{
			name:     "new destination explicitly clears credentials",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": ""},
		},
		{
			name:     "Telegram chat_id change is not an API destination change",
			kind:     KindTelegram,
			stored:   map[string]any{"bot_token": "t", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming: map[string]any{"chat_id": "-100200"},
		},
		{
			name:     "email recipient change is not a server destination change",
			kind:     KindEmail,
			stored:   map[string]any{"host": "smtp.corp.com", "port": 587, "password": "PW", "from": "a@b.c", "to": []any{"x@y.z"}},
			incoming: map[string]any{"to": []any{"new@y.z"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err != nil {
				t.Fatalf("Legitimate edit incorrectly blocked: %v", err)
			}
			if merged == nil {
				t.Fatal("Expected merged configuration")
			}
		})
	}
}

// TestPrepareConfigUpdatePortTypeTolerance avoids false destination changes when equal ports use int
// versus JSON float64. False password-reentry warnings on unrelated edits would undermine confidence
// in the guard.
func TestPrepareConfigUpdatePortTypeTolerance(t *testing.T) {
	stored := map[string]any{"host": "smtp.corp.com", "port": float64(587), "password": "PW"}
	// Submit the same port as an int.
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 587}); err != nil {
		t.Fatalf("Equal port values with different types must not count as destination changes: %v", err)
	}
	// An actual port change must still be blocked.
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 25}); err == nil {
		t.Fatal("Port change must be blocked")
	}
}

// TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination covers optional Telegram base_url.
// Creation stores an empty string; first merge deletes it; later saves must treat missing and empty as
// equivalent. Previously unchanged masked tokens then caused permanent save failures until re-entered.
func TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination(t *testing.T) {
	stored := map[string]any{"bot_token": "123:ABC", "chat_id": "-100", "base_url": ""}

	// Reproduce frontend buildConfig output: every field is submitted, including masked secrets and empty
	// text boxes, rather than only changed keys.
	submit := func() map[string]any {
		return map[string]any{
			"bot_token": MaskedValue("123:ABC"),
			"chat_id":   "-100",
			"base_url":  "",
		}
	}

	// First save changes only the channel name and resubmits configuration unchanged.
	merged, err := PrepareConfigUpdate(KindTelegram, stored, submit())
	if err != nil {
		t.Fatalf("First save incorrectly blocked: %v", err)
	}
	if _, ok := merged["base_url"]; ok {
		t.Fatal("Precondition changed: MergeConfig must delete the empty value so this test covers subsequent missing-key saves")
	}

	// Second save submits identical content with no user changes.
	merged2, err := PrepareConfigUpdate(KindTelegram, merged, submit())
	if err != nil {
		t.Fatalf("Second unchanged save incorrectly blocked: %v", err)
	}
	// A third save proves repeated saves remain stable.
	if _, err := PrepareConfigUpdate(KindTelegram, merged2, submit()); err != nil {
		t.Fatalf("Third save incorrectly blocked: %v", err)
	}
	// Credentials must survive throughout and not be cleared by empty-field handling.
	if got := merged2["bot_token"]; got != "123:ABC" {
		t.Fatalf("Bot token must preserve the original value, got %v", got)
	}
}

// TestPrepareConfigUpdateStillGuardsBlankDestinationChanges ensures empty/missing equivalence does not
// bypass genuine destination changes. Telegram tokens travel in URL paths, so changing base_url in
// either direction changes their recipient.
func TestPrepareConfigUpdateStillGuardsBlankDestinationChanges(t *testing.T) {
	// Change from empty (official API) to a custom address.
	official := map[string]any{"bot_token": "123:ABC", "chat_id": "-100"}
	if _, err := PrepareConfigUpdate(KindTelegram, official, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "https://tg-proxy.attacker.tld",
	}); err == nil {
		t.Fatal("Switching from official to custom API must require token re-entry")
	}

	// Clearing a custom address returns to the official API and is also a destination change.
	proxied := map[string]any{"bot_token": "123:ABC", "base_url": "https://proxy.internal/bot"}
	if _, err := PrepareConfigUpdate(KindTelegram, proxied, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "",
	}); err == nil {
		t.Fatal("Clearing custom API is also a destination change and must require token re-entry")
	}
}

// TestPrepareConfigUpdateSwitchAddedAfterSave covers a destination switch the stored configuration
// predates (email allow_plaintext). The form submits false for it on every save; that must not demand
// the password again, while actually turning the switch on or off still does.
func TestPrepareConfigUpdateSwitchAddedAfterSave(t *testing.T) {
	stored := map[string]any{"host": "smtp.corp.com", "port": float64(587), "tls": false, "password": "PW"}
	submit := func(plaintext bool) map[string]any {
		return map[string]any{"host": "smtp.corp.com", "port": 587, "tls": false, "allow_plaintext": plaintext, "password": MaskedValue("PW")}
	}
	if _, err := PrepareConfigUpdate(KindEmail, stored, submit(false)); err != nil {
		t.Fatalf("Unchanged save of a channel stored before the switch existed was blocked: %v", err)
	}
	if _, err := PrepareConfigUpdate(KindEmail, stored, submit(true)); err == nil {
		t.Fatal("Allowing plaintext delivery must require password re-entry")
	}
	stored["allow_plaintext"] = true
	if _, err := PrepareConfigUpdate(KindEmail, stored, submit(false)); err == nil {
		t.Fatal("Turning plaintext delivery off must require password re-entry")
	}
}

func TestDestinationKeysDeclaredForEveryKind(t *testing.T) {
	// Like SecretKeys, every adapter must declare destination fields or PrepareConfigUpdate cannot protect
	// it.
	for kind, ch := range registry {
		if len(ch.DestinationKeys()) == 0 {
			t.Errorf("Channel %s has no destination keys; destination-swap protection cannot apply", kind)
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("Channel %s has no credential keys", kind)
		}
	}
}

func TestSecretKeysDeclaredForEveryKind(t *testing.T) {
	// The compiler requires SecretKeys; also ensure no adapter returns an empty list, which would expose
	// its credentials to browsers.
	expect := map[string]bool{
		KindDingTalk: true, KindFeishu: true, KindWeCom: true,
		KindWebhook: true, KindTelegram: true, KindEmail: true,
	}
	for kind, ch := range registry {
		if !expect[kind] {
			t.Errorf("Channel %s has no masking expectations in this test", kind)
			continue
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("Channel %s declares no secrets and would expose plaintext configuration", kind)
		}
	}
}

// TestPrepareConfigUpdateRejectsMaskedInContainer covers nested mask sentinels in objects such as
// webhook.headers. MergeConfig only recognizes whole-string masks; storing a nested literal __masked__
// would silently break later authentication.
func TestPrepareConfigUpdateRejectsMaskedInContainer(t *testing.T) {
	stored := map[string]any{
		"url":     "https://legit.example.com/hook",
		"headers": map[string]any{"Authorization": "Bearer REAL"},
	}
	// Mask sentinel embedded inside an object.
	incoming := map[string]any{
		"headers": map[string]any{"Authorization": MaskedPrefix},
	}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, incoming); err == nil {
		t.Fatal("Nested mask sentinels must be rejected instead of stored literally")
	}
	// A complete object containing genuine replacement values remains accepted.
	ok := map[string]any{"headers": map[string]any{"Authorization": "Bearer NEW"}}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, ok); err != nil {
		t.Fatalf("Valid replacement headers must remain accepted: %v", err)
	}
}
