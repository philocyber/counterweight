package authz

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var updateFixtures = flag.Bool("update", false, "rewrite testdata/editor-document.json")

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func documentJSON(t *testing.T, policy []byte) []byte {
	t.Helper()
	doc, ok := Outline(policy)
	if !ok {
		t.Fatal("the policy has no document")
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// A policy handed to the console's matrix editor and written back by it must
// be the same policy. editor-emitted.yaml is the editor's real output for
// editor-document.json (web/src/lib/authz-policy.test.mjs keeps it current),
// so this closes the loop across the two languages.
func TestDocumentSurvivesTheMatrixEditor(t *testing.T) {
	source := readFixture(t, "editor-source.yaml")
	if _, err := Parse(source); err != nil {
		t.Fatalf("the source fixture is not a valid policy:\n%v", err)
	}
	want := documentJSON(t, source)
	if *updateFixtures {
		if err := os.WriteFile("testdata/editor-document.json", want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFixture(t, "editor-document.json"); !bytes.Equal(got, want) {
		t.Fatal("testdata/editor-document.json is out of date; run: go test ./authz -run TestDocument -update, then UPDATE_FIXTURES=1 npm test in web")
	}
	emitted := readFixture(t, "editor-emitted.yaml")
	if _, err := Parse(emitted); err != nil {
		t.Fatalf("the editor wrote a policy that does not validate:\n%v", err)
	}
	if got := documentJSON(t, emitted); !bytes.Equal(got, want) {
		t.Errorf("the policy changed on its way through the editor.\nbefore:\n%s\nafter:\n%s", want, got)
	}
}

func TestOutline(t *testing.T) {
	const head = "version: 1\ntarget: { base_url: \"https://staging.example.test\" }\npersonas: { anonymous: {} }\ncases:\n"
	body := func(json string) string {
		return head + "  - id: a\n    allow_write: true\n    request: { method: POST, path: /a, json: " + json + " }\n    expect: { anonymous: { deny: 401 } }\n"
	}
	for name, tc := range map[string]struct {
		policy string
		ok     bool
	}{
		// An editor must show a policy that still has problems.
		"problems to fix":         {"version: 1\ntarget: { base_url: \"\" }\npersonas: { admin: { login: { request: { method: POST, path: /login } } } }\ncases: []\n", true},
		"plain numbers":           {body("{ n: 9007199254740991, f: 1.5, s: \"9007199254740993\" }"), true},
		"not a policy":            {"just text", false},
		"unknown field":           {head + "extra: 1\n", false},
		"two documents":           {body("{}") + "---\nversion: 1\n", false},
		"number a browser rounds": {body("{ n: 9007199254740993 }"), false},
		"nested, in a list":       {body("{ list: [1, { n: -9007199254740993 }] }"), false},
		"key that is not text":    {body("{ 1: a }"), false},
		"date":                    {body("{ day: 2026-01-02 }"), false},
		"empty case":              {head + "  -\n", false},
	} {
		if _, ok := Outline([]byte(tc.policy)); ok != tc.ok {
			t.Errorf("%s: ok=%v, want %v", name, ok, tc.ok)
		}
	}
}
