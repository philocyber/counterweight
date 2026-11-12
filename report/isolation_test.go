package report

import (
	"encoding/json"
	"github.com/Philocyber/counterweight/execution"
	"strings"
	"testing"
)

func TestExecutionIsolationReportLine(t *testing.T) {
	for _, info := range []execution.Info{{Mode: execution.ConfinedFiles, Capabilities: execution.Caps(execution.ConfinedFiles)}, {Mode: execution.Host}, {Mode: execution.Host, Mixed: true}, {Mode: execution.Host, Assumed: true}} {
		in := Input{Isolation: info}
		text := info.Description()
		if !strings.Contains(Markdown(in), text) {
			t.Fatal("Markdown omitted execution isolation")
		}
		data, err := SARIFFromInput(in, nil)
		if err != nil {
			t.Fatal(err)
		}
		var log map[string]any
		if err = json.Unmarshal(data, &log); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), text) {
			t.Fatal("SARIF omitted execution isolation")
		}
		w := pdfWriter{in: in}
		if w.in.ExecutionIsolation().Description() != text {
			t.Fatal("PDF model omitted execution isolation")
		}
		if _, err = PDF(in, PDFOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}
