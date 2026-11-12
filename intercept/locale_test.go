package intercept

import (
	"github.com/Philocyber/counterweight/locale"
	"strings"
	"testing"
)

func TestVerdictLanguageContractsRemainStrict(t *testing.T) {
	for _, input := range []string{
		`{"decision":"allow","comment":"Operation: read file; Effect if successful: return text; Rule: A5"}`,
		`{"decision":"deny","comment":"Operation: delete orders; Effect if successful: data loss; Rule: D4"}`,
	} {
		if v := ParseVerdict(input); v.Action == "" {
			t.Fatalf("valid language rejected: %s", input)
		}
	}
	for _, input := range []string{
		`{"decision":"allow","comment":"Operation: read; Effect if successful: ; Rule: A5"}`,
		`{"decision":"allow","comment":"Operation: read; Effect if successful: text; Rule: "}`,
		`{"decision":"allow","comment":"Operation: read; Effect if successful: text; Rule: A5","extra":"bypass"}`,
		`{"decision":"ALLOW","comment":"Operation: read; Effect if successful: text; Rule: A5"}`,
	} {
		if v := ParseVerdict(input); v.Action != "" {
			t.Fatalf("invalid contract accepted: %s", input)
		}
	}
}

func TestJudgePromptLanguagesPreservePolicyAndCustomText(t *testing.T) {
	for _, lang := range []locale.Lang{locale.En} {
		p := EffectiveJudgePrompt(DefaultJudgePrompt, lang)
		for _, rule := range []string{"D1", "D2", "D3", "D4", "D5", "D6", "A1", "A2", "A3", "A4", "A5", "A6", "ASK", "DEFAULT", "tool_name", "arguments"} {
			if !strings.Contains(p, rule) {
				t.Fatalf("%s prompt lost %s", lang, rule)
			}
		}
		contract := locale.Text(lang, JudgeOutputContract)
		if strings.Count(p, contract) != 1 {
			t.Fatalf("%s duplicated/missing output contract", lang)
		}
		custom := "Custom policy café"
		if got := EffectiveJudgePrompt(custom, lang); !strings.HasPrefix(got, custom) {
			t.Fatal("custom policy rewritten")
		}
	}
}
