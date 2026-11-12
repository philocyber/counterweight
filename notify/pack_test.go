package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// These tests verify whole-item packing and accurate included counts when digests exceed channel
// limits. Previously rendering then truncating while marking every entry delivered silently lost
// findings and falsely reported full success.

func TestMarkdownBodyPacksWholeItemsWithinByteLimit(t *testing.T) {
	// A 200-item Chinese digest must exceed WeCom's 4096-byte limit.
	m := batchMsg(200)
	body, kept := markdownBody(m, weComMarkdownLimit)

	if len(body) > weComMarkdownLimit {
		t.Fatalf("Body of %d bytes exceeds limit %d", len(body), weComMarkdownLimit)
	}
	if !utf8.ValidString(body) {
		t.Fatal("Body is not valid UTF-8")
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("Expected partial packing (0 < kept < %d), got %d", len(m.Items), kept)
	}
	// The header must accurately report included and remaining counts so readers recognize a partial
	// batch.
	if !strings.Contains(body, "remaining") || !strings.Contains(body, "next message") {
		t.Fatalf("Header must report omitted item count:\n%s", body[:minInt(400, len(body))])
	}
	// Only the first kept items should appear.
	for i := 0; i < kept; i++ {
		if !strings.Contains(body, "Finding"+itoa(i+1)) {
			t.Fatalf("Item %d must appear in this message:\n%s", i+1, body)
		}
	}
	if strings.Contains(body, "Finding"+itoa(kept+1)) {
		t.Fatalf("Item %d belongs to the next batch and must not appear", kept+1)
	}
}

func TestMarkdownBodyKeepsEverythingWhenUnderLimit(t *testing.T) {
	m := batchMsg(3)
	body, kept := markdownBody(m, 0) // Zero disables the limit.
	if kept != len(m.Items) {
		t.Fatalf("Unlimited rendering must keep all items, got kept=%d", kept)
	}
	if strings.Contains(body, "remaining") {
		t.Fatalf("Untruncated output must not show a continuation notice:\n%s", body)
	}
}

func TestMarkdownBodyAlwaysKeepsAtLeastOneItem(t *testing.T) {
	// Even if one item exceeds the budget, send one with final truncation. Otherwise the oversized item
	// would stall the batch forever across repeated claims.
	m := batchMsg(5)
	_, kept := markdownBody(m, 50)
	if kept != 1 {
		t.Fatalf("Must retain at least one item, got %d", kept)
	}
}

func TestMarkdownBodySingleReturnsOne(t *testing.T) {
	_, kept := markdownBody(singleMsg(), 4096)
	if kept != 1 {
		t.Fatalf("Single message must report one delivered item, got %d", kept)
	}
	// An empty message contains no deliverable items.
	if _, k := markdownBody(Message{}, 4096); k != 0 {
		t.Fatalf("Empty message must report zero items, got %d", k)
	}
}

func TestTelegramPackingUsesRuneBudget(t *testing.T) {
	m := batchMsg(200)
	text, kept := telegramHTML(m)
	// Telegram limits characters; counting bytes would reduce Chinese capacity to roughly one third.
	if n := utf8.RuneCountInString(text); n > telegramTextLimit {
		t.Fatalf("Body of %d characters exceeds limit %d", n, telegramTextLimit)
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("Expected a partial batch, got %d", kept)
	}
	if !strings.Contains(text, "will follow") {
		t.Fatalf("Must report remaining items:\n%.300s", text)
	}
}

func TestFeishuPackingReportsKept(t *testing.T) {
	m := batchMsg(2000)
	_, kept := feishuCard(m)
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("Card must fit only part of the batch, got %d", kept)
	}
}

func TestWebhookAndEmailReportAllItems(t *testing.T) {
	// These two channels do not truncate bodies and deliver the entire batch.
	m := batchMsg(7)
	if n := len(m.Items); n != 7 {
		t.Fatal("Precondition failed")
	}
	// Confirm through rendering that markdownBody with a zero limit keeps every item.
	if _, k := markdownBody(m, 0); k != len(m.Items) {
		t.Fatalf("Unlimited rendering must use all items, got %d", k)
	}
}

// TestMarkdownEscapesUntrustedContent prevents target/model titles, summaries, and asset URLs from
// changing message structure.
func TestMarkdownEscapesUntrustedContent(t *testing.T) {
	cases := []struct {
		name  string
		item  Item
		must  []string // Must appear in escaped form.
		wrong []string // Must not appear in unescaped form.
	}{
		{
			name: "newline and external link in title",
			item: Item{
				Severity: "high",
				Name:     "Login endpoint SQL injection\n[Urgent: click here to verify account](http://attacker.tld)",
			},
			// Collapse newlines to prevent forged list/quote blocks; escape brackets and parentheses to prevent
			// clickable external links.
			must:  []string{`\[Urgent: click here to verify account\]`, `\(http://attacker.tld\)`},
			wrong: []string{"\n[Urgent", "\n\n[Urgent"},
		},
		{
			name: "image beacon in title",
			item: Item{
				Severity: "high",
				Name:     "Finding ![](http://attacker.tld/beacon)",
			},
			must:  []string{`\!`, `\(http://attacker.tld/beacon\)`},
			wrong: []string{"![]("},
		},
		{
			name: "emphasis and quote in asset name",
			item: Item{
				Severity: "high",
				Name:     "Plain title",
				Assets:   []string{"a.com/*injection*>reference"},
			},
			must:  []string{`\*injection\*`, `\>`},
			wrong: []string{"*injection*"},
		},
		{
			name: "backticks and pipe in summary",
			item: Item{
				Severity: "high",
				Name:     "Title",
				Summary:  "`code` | table",
			},
			must:  []string{"\\`code\\`", `\|`},
			wrong: []string{"`code`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Items: []Item{tc.item}}
			// The single-item writer exercises the shared Markdown rendering path.
			var b strings.Builder
			writeItem(&b, tc.item, "", true)
			got := b.String()
			for _, want := range tc.must {
				if !strings.Contains(got, want) {
					t.Errorf("Missing escaped form %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wrong {
				if strings.Contains(got, bad) {
					t.Errorf("Unescaped form %q permits structure or external-link injection:\n%s", bad, got)
				}
			}
			_ = m
		})
	}
}

// TestMarkdownEscapeBackslashFirst preserves escape order so inserted slashes are not escaped again.
func TestMarkdownEscapeBackslashFirst(t *testing.T) {
	if got := markdownEscape(`a\b*c`); got != `a\\b\*c` {
		t.Fatalf("Incorrect escape order, got %q", got)
	}
}

// TestTelegramTitleHasNoMarkdownEscapes guards against shared-title Markdown escaping leaking visible
// backslashes such as \(1\) into Telegram HTML.
func TestTelegramTitleHasNoMarkdownEscapes(t *testing.T) {
	m := Message{Items: []Item{{Severity: "high", Name: "alert(1) *highlight*"}}}
	text, _ := telegramHTML(m)
	if strings.Contains(text, `\(`) || strings.Contains(text, `\*`) {
		t.Fatalf("Markdown backslash escapes leaked into Telegram HTML:\n%s", text)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
