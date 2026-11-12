package notify

import (
	"net/url"
	"testing"
	"time"
)

// Signature reference values were independently computed with OpenSSL, not this implementation, so
// they verify algorithms rather than merely unchanged code. With TS=1700000000000 and
// SECRET=SECtest123: DingTalk signs timestamp + newline + secret using secret as the HMAC-SHA256 key,
// yielding w3RMHXzixTMdzr8OHJUmVLS4IoPJVdu+Ut1LE48MePE=. Feishu signs an empty message using timestamp
// + newline + secret as key, yielding Hd4xFWQU6R6ad4nzy4ETIznzlqebqH7xcTFVmONTudo=.
// Reproduce the references with OpenSSL:
//
//	TS=1700000000000; SECRET=SECtest123
//	printf '%s\n%s' "$TS" "$SECRET" | openssl dgst -sha256 -hmac "$SECRET" -binary | openssl base64 -A
//	printf '' | openssl dgst -sha256 -hmac "$(printf '%s\n%s' "$TS" "$SECRET")" -binary | openssl base64 -A
const (
	signTestTSMillis = int64(1700000000000)
	signTestSecret   = "SECtest123"
	dingTalkExpected = "w3RMHXzixTMdzr8OHJUmVLS4IoPJVdu+Ut1LE48MePE="
	feishuExpected   = "Hd4xFWQU6R6ad4nzy4ETIznzlqebqH7xcTFVmONTudo="
)

func TestDingTalkSignMatchesReference(t *testing.T) {
	got, err := dingTalkSignedURL("https://oapi.dingtalk.com/robot/send?access_token=tok", signTestSecret, time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatalf("Signing failed: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("Generated URL cannot be parsed: %v", err)
	}
	q := u.Query()
	if q.Get("sign") != dingTalkExpected {
		t.Errorf("Signature mismatch\nWant %s\nGot %s", dingTalkExpected, q.Get("sign"))
	}
	if q.Get("timestamp") != "1700000000000" {
		t.Errorf("Timestamp must be preserved in milliseconds, got %q", q.Get("timestamp"))
	}
	// Signing must preserve existing query parameters such as access_token.
	if q.Get("access_token") != "tok" {
		t.Errorf("Original query parameter lost, got %q", q.Get("access_token"))
	}
}

func TestFeishuSignMatchesReference(t *testing.T) {
	got := feishuSign("1700000000000", signTestSecret)
	if got != feishuExpected {
		t.Errorf("Signature mismatch\nWant %s\nGot %s", feishuExpected, got)
	}
}

// TestSignAlgorithmsDiffer prevents merging incompatible algorithms: DingTalk keys HMAC with secret,
// while Feishu keys it with the signing string. Copying either algorithm to the other fails
// verification.
func TestSignAlgorithmsDiffer(t *testing.T) {
	ts := "1700000000000"
	dingURL, err := dingTalkSignedURL("https://example.com/hook", signTestSecret, time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatal(err)
	}
	dq, _ := url.Parse(dingURL)
	if dq.Query().Get("sign") == feishuSign(ts, signTestSecret) {
		t.Fatal("DingTalk and Feishu signatures match; at least one algorithm is wrong")
	}
}

func TestDingTalkNoSecretLeavesURLUntouched(t *testing.T) {
	// Unsigned bots must not acquire timestamp or sign parameters.
	const hook = "https://oapi.dingtalk.com/robot/send?access_token=tok"
	got, err := dingTalkSignedURL(hook, "", time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatal(err)
	}
	if got != hook {
		t.Fatalf("URL must remain unchanged without secret, got %q", got)
	}
}

func TestValidateHTTPURL(t *testing.T) {
	ok := []string{"https://example.com/hook", "http://10.0.0.1:8080/x?y=1"}
	for _, s := range ok {
		if err := validateHTTPURL(s); err != nil {
			t.Errorf("%q must be accepted: %v", s, err)
		}
	}
	// Reject schemes such as file:// outside the intended http.Client behavior.
	bad := []string{"", "file:///etc/passwd", "ftp://example.com", "https://", "gopher://x"}
	for _, s := range bad {
		if err := validateHTTPURL(s); err == nil {
			t.Errorf("%q must be rejected", s)
		}
	}
}
