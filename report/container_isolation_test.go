package report

import (
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/execution"
)

func TestContainerExecutionReportStatesLimits(t *testing.T) {
	in := Input{Isolation: execution.Info{Mode: execution.Container, Capabilities: execution.Caps(execution.Container), ImageDigest: "sha256:fixture"}}
	md := Markdown(in)
	sarif, err := SARIFFromInput(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"commands run in a container", "not restricted", "Traffic capture does not record"} {
		if !strings.Contains(md, s) || !strings.Contains(string(sarif), s) {
			t.Fatalf("export omitted %q", s)
		}
	}
	if !strings.Contains(string(sarif), "sha256:fixture") {
		t.Fatal("SARIF omitted image provenance")
	}
	if _, err = PDF(in, PDFOptions{}); err != nil {
		t.Fatal(err)
	}
}
