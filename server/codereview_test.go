package server

import (
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/locale"
)

func TestParseCodeReviewCommand(t *testing.T) {
	// Not the command.
	if _, _, isCmd, _ := parseCodeReviewCommand("hello there"); isCmd {
		t.Fatal("plain message detected as command")
	}

	// Valid, explicit mode.
	repo, mode, isCmd, err := parseCodeReviewCommand("/code-review Philocyber/counterweight comprehensive")
	if !isCmd || err != nil {
		t.Fatalf("valid command rejected: isCmd=%v err=%v", isCmd, err)
	}
	if repo.Slug() != "Philocyber/counterweight" || mode != codesrc.ModeComprehensive {
		t.Fatalf("parsed %q / %q", repo.Slug(), mode)
	}

	// Valid, default mode = balanced.
	_, mode, _, err = parseCodeReviewCommand("/code-review github.com/a/b")
	if err != nil || mode != codesrc.ModeBalanced {
		t.Fatalf("default mode: mode=%q err=%v", mode, err)
	}

	// Command but missing repo → usage error, still flagged as command.
	if _, _, isCmd, err := parseCodeReviewCommand("/code-review"); !isCmd || err == nil {
		t.Fatalf("missing repo: isCmd=%v err=%v", isCmd, err)
	}

	// Command with bad mode → error.
	if _, _, isCmd, err := parseCodeReviewCommand("/code-review a/b deep"); !isCmd || err == nil {
		t.Fatalf("bad mode should error: isCmd=%v err=%v", isCmd, err)
	}

	// Command with non-GitHub host → error.
	if _, _, isCmd, err := parseCodeReviewCommand("/code-review https://gitlab.com/a/b"); !isCmd || err == nil {
		t.Fatalf("non-github host should error: isCmd=%v err=%v", isCmd, err)
	}
}

func TestComposeCodeReviewGoalMentionsPathAndMode(t *testing.T) {
	repo := codesrc.Repo{Owner: "a", Name: "b", Ref: "dev"}
	goal := composeCodeReviewGoal(repo, codesrc.ModeLight, "/work/src", locale.ServerDefault())
	for _, want := range []string{"a/b", "dev", "/work/src", "light", "whitebox-code-review-methodology", "report_finding"} {
		if !strings.Contains(goal, want) {
			t.Errorf("composed goal missing %q: %s", want, goal)
		}
	}
}

func TestInferCodeReviewFromTaskText(t *testing.T) {
	// Text of a white-box task created by hand (no /code-review launch record).
	cr := inferCodeReview("Argus Whitebox Test", "Full white-box security assessment of philocyber/argus-tm",
		"Conduct an authorized comprehensive white-box security assessment of the source repository https://github.com/philocyber/argus-tm. Scope is this repository and local/test environments only.")
	if cr == nil || cr.Repository != "philocyber/argus-tm" || !cr.Inferred || cr.Commit != "" || cr.Mode != "" {
		t.Fatalf("white-box task not inferred correctly: %+v", cr)
	}
	for name, texts := range map[string][]string{
		"pentest naming a repo":    {"Web app test", "Assess https://shop.lab.test; source at https://github.com/acme/shop"},
		"review of two repos":      {"Code review", "Review github.com/acme/api and github.com/acme/web"},
		"white-box without a repo": {"White-box review", "Review the attached archive"},
	} {
		if cr := inferCodeReview(texts...); cr != nil {
			t.Errorf("%s: unexpectedly inferred %+v", name, cr)
		}
	}
}
