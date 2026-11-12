package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// Valid UTF-8 is essential under WeCom's byte limit. € occupies three bytes; naive
	// slicing splits it, the same way it would split CJK content. Mixed inputs with
	// differing lengths exercise every possible boundary.
	inputs := []string{
		"€€€€€€",
		"mixed € content €",
		"a€b€c€d€e",
		"🔴🟠🟡🔵", // Four-byte emoji make boundary errors especially visible.
		strings.Repeat("€€", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("Input %q max=%d: invalid UTF-8 output %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("Input %q max=%d: output of %d bytes exceeds limit", in, max, len(got))
			}
			// Untruncated content must remain unchanged.
			if len(in) <= max && got != in {
				t.Fatalf("Input %q max=%d: unchanged-size content was modified -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 must disable the limit")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 must disable the limit")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// A budget smaller than the ellipsis must not overflow merely to append it.
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("max=1: output %q of length %d exceeds limit", got, len(got))
	}
	// Normal truncation should include an ellipsis.
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("Expected ellipsis, got %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// Preserve the distinction from TruncateBytes: Telegram counts characters, and byte counting would cut
	// three-byte content to one third.
	s := "€€€€€€€€€€"
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("Expected 5 characters, got %d (%q)", n, got)
	}
	// The same input truncated by bytes should be substantially shorter.
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("Byte and character budgets must produce different character counts")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("First line\n\nSecond line\thas tabs   many spaces", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("All whitespace must collapse, got %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("Consecutive spaces must collapse, got %q", got)
	}
	// Truncated output must remain readable and valid.
	got = OneLine("€€€€€€€€€€", 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("Expected 4 characters, got %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// Naive HTML truncation can leave an incomplete href tag and cause rejection of the whole message.
	s := `<b>title</b>bodybodybody<a href="https://example.com/very/long/path">view details</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: output of %d characters exceeds limit", max, n)
		}
		// No trailing opening angle bracket may remain without its closing bracket.
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: trailing tag was split -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("No assets must return empty text, got %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("All assets within limit must appear, got %q", got)
	}
	// When assets exceed the limit, show the total so readers know entries were omitted.
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "5 total") {
		t.Fatalf("Total count 5 must appear, got %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("Empty severity ranks zero and must fail a threshold")
	}
	if !AtLeast("critical", "") {
		t.Fatal("Empty threshold must accept")
	}
	if got := StatusLabel("fixed"); got != "Fixed" {
		t.Fatalf("Incorrect status mapping, got %q", got)
	}
	// Unknown statuses remain verbatim without invented labels.
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("Unknown status must remain verbatim, got %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity covers incomplete entities as well as incomplete tags. Cutting &amp;
// to &amp can make strict parsers reject an entire otherwise routine oversized digest.
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// No trailing ampersand may remain without its matching semicolon.
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: trailing entity fragment %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: malformed entity", max)
		}
	}
}
