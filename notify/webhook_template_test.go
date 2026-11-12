package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// These tests lock down webhook-template capabilities, the only place user strings are evaluated as
// code. Adding context methods or FuncMap helpers such as readFile could silently expand access
// despite appearing to be harmless refactoring.

// TestTemplateContextHasNoMethods enforces pure-data contexts. text/template invokes exported methods
// as well as fields, so any reachable method becomes available to authors. A failure means
// webhookTemplateData/webhookItem gained methods; review possible data exposure before permitting
// them.
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s exposes %d methods (%s): text/template can invoke them,"+
				" exposing their capabilities to template authors", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal fixes the helper set to json/jsons serialization. Each additional
// function is a new capability; templates must not read files, send requests, or execute commands.
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Template functions changed: got %v, want %v. Review new capabilities"+
			" (no file access, network requests, or command execution)", got, want)
	}
}

// TestTemplateCannotReachUnknownData requires out-of-scope access to fail without exposing internal
// data.
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("Unknown fields must return an error")
	}
	// Errors must not contain actual finding titles or summaries from the context.
	for _, leak := range []string{"SQL injection", "parameter id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("Template error leaked message content %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently treats bad templates as configuration failures; retries cannot
// fix them and would waste repeated backoff cycles.
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("Template syntax errors must fail before saving")
	}
	// Even direct delivery that bypasses validation must classify template failures as permanent.
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Invalid template must fail permanently, got %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON requires valid JSON output and prevents templates from emitting
// arbitrary text to trigger other protocols.
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// A valid template must succeed.
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("Valid template must pass validation: %v", err)
	}
	// Reject non-JSON output instead of sending it unchanged.
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Non-JSON output must fail permanently, got %v", err)
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("Error must identify the JSON problem, got %v", err)
	}
}
