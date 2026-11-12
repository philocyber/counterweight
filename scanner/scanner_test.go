package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for a scanner executable, so Run is
// exercised without any real scanner. The fake is selected by environment.
func TestMain(m *testing.M) {
	if os.Getenv("SCANNER_FAKE") == "1" {
		fakeScanner()
		return
	}
	os.Exit(m.Run())
}

func fakeScanner() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println("fake 1.2.3")
		os.Exit(0)
	}
	if rec := os.Getenv("FAKE_RECORD"); rec != "" {
		cwd, _ := os.Getwd()
		_ = os.WriteFile(rec, []byte(strings.Join(args, "\n")+"\n--cwd--\n"+cwd), 0o600)
		cfg := ""
		for i, a := range args {
			if a == "--config" && i+1 < len(args) {
				if b, err := os.ReadFile(args[i+1]); err == nil {
					cfg = string(b)
				}
			}
		}
		_ = os.WriteFile(rec+".cfg", []byte(cfg), 0o600)
	}
	if ms, _ := strconv.Atoi(os.Getenv("FAKE_SLEEP_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if n, _ := strconv.Atoi(os.Getenv("FAKE_BYTES")); n > 0 {
		buf := []byte(strings.Repeat("x", 1<<20))
		for written := 0; written < n; written += len(buf) {
			os.Stdout.Write(buf)
		}
	} else {
		fmt.Print(os.Getenv("FAKE_OUT"))
	}
	fmt.Fprint(os.Stderr, os.Getenv("FAKE_ERR"))
	code, _ := strconv.Atoi(os.Getenv("FAKE_EXIT"))
	os.Exit(code)
}

func fakeEnv(kv ...string) []string {
	env := append(BaseEnv(), "SCANNER_FAKE=1")
	return append(env, kv...)
}

// A multi-package OSV result: lodash carries a GHSA and its CVE (one lead), a
// second advisory, minimist sits in another lockfile, and a Python package has
// no fixed version.
const osvFixture = `{"results":[
 {"source":{"path":"/work/repo/web/package-lock.json","type":"lockfile"},"packages":[
  {"package":{"name":"lodash","version":"4.17.15","ecosystem":"npm"},
   "vulnerabilities":[
    {"id":"GHSA-p6mc-m468-83gw","aliases":["CVE-2020-8203"],"summary":"Prototype Pollution in lodash",
     "affected":[{"package":{"name":"lodash","ecosystem":"npm"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"4.17.19"}]}]}]},
    {"id":"CVE-2020-8203","aliases":["GHSA-p6mc-m468-83gw"],"summary":"Prototype Pollution in lodash",
     "affected":[{"package":{"name":"lodash","ecosystem":"npm"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"4.17.20"}]}]}]},
    {"id":"GHSA-35jh-r3h4-6jhm","aliases":["CVE-2021-23337"],"summary":"Command Injection in lodash",
     "affected":[{"package":{"name":"lodash","ecosystem":"npm"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"4.17.21"}]}]}]}],
   "groups":[{"ids":["GHSA-p6mc-m468-83gw","CVE-2020-8203"],"aliases":["CVE-2020-8203"],"max_severity":"7.4"},{"ids":["GHSA-35jh-r3h4-6jhm"],"max_severity":"7.2"}]},
  {"package":{"name":"minimist","version":"1.2.0","ecosystem":"npm"},
   "vulnerabilities":[{"id":"GHSA-xvch-5gv4-984h","summary":"Prototype Pollution in minimist",
     "affected":[{"package":{"name":"minimist","ecosystem":"npm"},"ranges":[{"events":[{"introduced":"1.0.0"},{"fixed":"1.2.6"}]}]}]}],
   "groups":[{"ids":["GHSA-xvch-5gv4-984h"]}]}]},
 {"source":{"path":"/work/repo/api/requirements.txt","type":"lockfile"},"packages":[
  {"package":{"name":"Django","version":"2.2.0","ecosystem":"PyPI"},
   "vulnerabilities":[{"id":"PYSEC-2019-1","summary":"","affected":[{"package":{"name":"django","ecosystem":"PyPI"},"ranges":[{"events":[{"introduced":"0"}]}]}]}]}]}
]}`

func TestParseOSVMultiPackage(t *testing.T) {
	leads, err := ParseOSV("/work/repo", []byte(osvFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(leads) != 4 {
		t.Fatalf("want 4 leads (aliases collapsed), got %d: %+v", len(leads), leads)
	}
	by := map[string]Lead{}
	for _, l := range leads {
		by[l.RuleID] = l
		if l.Kind != KindAdvisory || l.Scanner != OSV || l.Fingerprint == "" {
			t.Fatalf("bad lead %+v", l)
		}
	}
	g := by["GHSA-p6mc-m468-83gw"]
	if g.Package != "lodash" || g.Version != "4.17.15" || g.File != "web/package-lock.json" || g.FixedVersion != "4.17.19" || g.Severity != 7.4 {
		t.Fatalf("lodash lead: %+v", g)
	}
	if len(g.Aliases) != 1 || g.Aliases[0] != "CVE-2020-8203" {
		t.Fatalf("aliases: %v", g.Aliases)
	}
	if by["GHSA-35jh-r3h4-6jhm"].FixedVersion != "4.17.21" || by["GHSA-xvch-5gv4-984h"].FixedVersion != "1.2.6" {
		t.Fatalf("fixed versions: %+v", by)
	}
	d := by["PYSEC-2019-1"]
	if d.File != "api/requirements.txt" || d.FixedVersion != "" || d.Title != "PYSEC-2019-1" {
		t.Fatalf("django lead: %+v", d)
	}
	for _, l := range leads {
		if strings.Contains(l.File, "/work/repo") {
			t.Fatalf("clone path leaked: %q", l.File)
		}
	}
}

func TestParseOSVEmptyAndInvalid(t *testing.T) {
	for _, in := range []string{"", "  \n", `{"results":[]}`, `{}`} {
		if leads, err := ParseOSV("/r", []byte(in)); err != nil || len(leads) != 0 {
			t.Fatalf("%q: %v %v", in, leads, err)
		}
	}
	if _, err := ParseOSV("/r", []byte("not json")); err == nil {
		t.Fatal("invalid JSON must be an error")
	}
}

// secretValue stands in for a credential. A real run redacts it, but the parser
// must not depend on that: fixture output plants it in every field that could
// carry matched text.
const secretValue = "AKIAZZPLANTEDSECRET7890"

const gitleaksFixture = `[
 {"RuleID":"aws-access-token","Description":"Identified a pattern that may indicate AWS credentials","StartLine":14,"EndLine":14,
  "StartColumn":9,"EndColumn":29,"Match":"aws_key = \"REDACTED\" ` + secretValue + `","Secret":"` + secretValue + `",
  "File":"/work/repo/config/prod.env","SymlinkFile":"","Commit":"","Entropy":3.9,"Author":"` + secretValue + `","Email":"","Date":"",
  "Message":"` + secretValue + `","Tags":["key","AWS"],"Fingerprint":"/work/repo/config/prod.env:aws-access-token:14"},
 {"RuleID":"generic-api-key","Description":"","StartLine":3,"File":"deploy/ci.yml","Match":"REDACTED","Secret":"REDACTED"},
 {"RuleID":"generic-api-key","Description":"","StartLine":3,"File":"deploy/ci.yml","Match":"REDACTED","Secret":"REDACTED"}
]`

func TestParseGitleaksRedacted(t *testing.T) {
	leads, err := ParseGitleaks("/work/repo", []byte(gitleaksFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(leads) != 2 {
		t.Fatalf("want 2 leads (exact duplicate dropped), got %d: %+v", len(leads), leads)
	}
	l := leads[0]
	if l.Kind != KindSecret || l.Scanner != Gitleaks || l.RuleID != "aws-access-token" || l.File != "config/prod.env" || l.Line != 14 {
		t.Fatalf("lead: %+v", l)
	}
	if leads[1].Title != "generic-api-key" {
		t.Fatalf("empty description falls back to the rule id: %+v", leads[1])
	}
	raw, _ := json.Marshal(leads)
	if strings.Contains(string(raw), secretValue) || strings.Contains(string(raw), "REDACTED") {
		t.Fatalf("matched text reached a lead: %s", raw)
	}
}

func TestParseGitleaksEmpty(t *testing.T) {
	for _, in := range []string{"", "[]", "null", "\n"} {
		if leads, err := ParseGitleaks("/r", []byte(in)); err != nil || len(leads) != 0 {
			t.Fatalf("%q: %v %v", in, leads, err)
		}
	}
	if _, err := ParseGitleaks("/r", []byte("{")); err == nil {
		t.Fatal("invalid JSON must be an error")
	}
}

func TestFingerprintsAreStableAndDistinct(t *testing.T) {
	a, _ := ParseGitleaks("/work/repo", []byte(gitleaksFixture))
	b, _ := ParseGitleaks("/other/clone", []byte(strings.ReplaceAll(gitleaksFixture, "/work/repo", "/other/clone")))
	if len(a) != len(b) || a[0].Fingerprint != b[0].Fingerprint || a[1].Fingerprint != b[1].Fingerprint {
		t.Fatalf("fingerprints must not depend on the clone location: %+v vs %+v", a, b)
	}
	if a[0].Fingerprint == a[1].Fingerprint {
		t.Fatal("different leads share a fingerprint")
	}
	moved := strings.Replace(gitleaksFixture, `"StartLine":14`, `"StartLine":15`, 1)
	c, _ := ParseGitleaks("/work/repo", []byte(moved))
	if c[0].Fingerprint == a[0].Fingerprint {
		t.Fatal("a different line must give a different fingerprint")
	}
	o1, _ := ParseOSV("/work/repo", []byte(osvFixture))
	o2, _ := ParseOSV("/x", []byte(strings.ReplaceAll(osvFixture, "/work/repo", "/x")))
	for i := range o1 {
		if o1[i].Fingerprint != o2[i].Fingerprint {
			t.Fatalf("osv fingerprint %d unstable", i)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	configured, managed, onPath := mk("configured"), mk("managed"), mk("onpath")
	look := func(string) (string, error) { return onPath, nil }
	noPath := func(string) (string, error) { return "", os.ErrNotExist }

	if r, ok := Resolve(configured, managed, "x", look); !ok || r.Path != configured || r.Source != "configured" {
		t.Fatalf("configured first: %+v %v", r, ok)
	}
	if r, ok := Resolve("", managed, "x", look); !ok || r.Path != managed || r.Source != "managed" {
		t.Fatalf("managed second: %+v %v", r, ok)
	}
	if r, ok := Resolve(filepath.Join(dir, "missing"), filepath.Join(dir, "nope"), "x", look); !ok || r.Path != onPath || r.Source != "path" {
		t.Fatalf("PATH last: %+v %v", r, ok)
	}
	if _, ok := Resolve(dir, filepath.Join(dir, "nope"), "x", noPath); ok {
		t.Fatal("a directory is not a binary, and nothing else exists: must be unavailable")
	}
	if _, ok := Resolve("", "", "x", noPath); ok {
		t.Fatal("no binary anywhere must be unavailable")
	}
}

func TestSpecsUseFixedArgumentsWithoutShell(t *testing.T) {
	g, _ := Find(Gitleaks)
	args := g.Args("/repo", "/work")
	joined := strings.Join(args, " ")
	for _, want := range []string{"dir /repo", "--redact", "--report-format json", "--report-path -", "--config " + filepath.Join("/work", "gitleaks.toml")} {
		if !strings.Contains(joined, want) {
			t.Fatalf("gitleaks args %q lack %q", joined, want)
		}
	}
	if strings.Contains(joined, "--no-git") || strings.Contains(joined, "log-opts") {
		t.Fatalf("history must not be scanned: %q", joined)
	}
	o, _ := Find(OSV)
	oargs := strings.Join(o.Args("/repo", "/work"), " ")
	if !strings.Contains(oargs, "--format json") || !strings.HasSuffix(oargs, "/repo") {
		t.Fatalf("osv args: %q", oargs)
	}
	// A hostile directory name stays one argument.
	if a := o.Args(`/r; rm -rf /`, "/w"); a[len(a)-1] != `/r; rm -rf /` {
		t.Fatalf("root must be a single argument: %v", a)
	}
}

func TestRunParsesAndRecordsVersion(t *testing.T) {
	spec, _ := Find(Gitleaks)
	res, err := Run(context.Background(), spec, os.Args[0], "/work/repo", fakeEnv("FAKE_OUT="+gitleaksFixture, "FAKE_EXIT=1"))
	if err != nil {
		t.Fatalf("exit 1 with output is a normal result: %v", err)
	}
	if len(res.Leads) != 2 || res.Version != "fake 1.2.3" {
		t.Fatalf("%+v", res)
	}
}

func TestRunUsesPrivateWorkDirAndOurConfig(t *testing.T) {
	spec, _ := Find(Gitleaks)
	rec := filepath.Join(t.TempDir(), "rec")
	repo := t.TempDir()
	if _, err := Run(context.Background(), spec, os.Args[0], repo, fakeEnv("FAKE_OUT=[]", "FAKE_RECORD="+rec)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(rec)
	cwd := string(got[strings.LastIndex(string(got), "--cwd--\n")+8:])
	if cwd == repo || strings.HasPrefix(cwd, repo) {
		t.Fatalf("scanner must not run inside the repository: %s", cwd)
	}
	if cfg, _ := os.ReadFile(rec + ".cfg"); !strings.Contains(string(cfg), "useDefault = true") {
		t.Fatalf("our configuration was not used: %q", cfg)
	}
	if _, err := os.Stat(cwd); err == nil {
		t.Fatal("work directory must be removed afterwards")
	}
}

func TestRunFailureModes(t *testing.T) {
	spec, _ := Find(OSV)
	ctx := context.Background()
	if _, err := Run(ctx, spec, os.Args[0], "/r", fakeEnv("FAKE_EXIT=2", "FAKE_ERR=boom in /home/user/x")); err == nil || !strings.Contains(err.Error(), "status 2") {
		t.Fatalf("unexpected exit status must fail: %v", err)
	}
	if res, err := Run(ctx, spec, os.Args[0], "/r", fakeEnv("FAKE_EXIT=128")); err != nil || len(res.Leads) != 0 {
		t.Fatalf("no package files is not a failure: %v %v", res, err)
	}
	if _, err := Run(ctx, spec, os.Args[0], "/r", fakeEnv("FAKE_OUT=garbage")); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("garbage output must fail: %v", err)
	}
	if _, err := Run(ctx, spec, filepath.Join(t.TempDir(), "missing"), "/r", fakeEnv()); err == nil {
		t.Fatal("a missing binary must fail")
	}
}

func TestRunOutputCapAndTimeout(t *testing.T) {
	spec, _ := Find(OSV)
	if _, err := Run(context.Background(), spec, os.Args[0], "/r", fakeEnv("FAKE_BYTES="+strconv.Itoa(OutputLimit+(2<<20)))); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("output over the cap must fail: %v", err)
	}
	spec.Timeout = 300 * time.Millisecond
	start := time.Now()
	if _, err := Run(context.Background(), spec, os.Args[0], "/r", fakeEnv("FAKE_SLEEP_MS=20000")); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a slow scanner must time out: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("timeout did not stop the process")
	}
}

// Real OSV data mixes commit hashes with release versions in the fixed events.
func TestFixedVersionKeepsReleasesOnly(t *testing.T) {
	const commits = "70f906c51ce49c485f1d355703e9cc3386b1cc2b, afd63b16170b7c047f5758eb910c416511e9c965"
	for _, c := range []struct{ installed, want string }{
		{"2.2.0", "2.2.5"}, // lowest release above the installed version
		{"2.2.9", "2.2.5"}, // none above: list what remains
		{"weird", "2.2.5"}, // not comparable: list releases
	} {
		got := PickFixed(c.installed, strings.Split(commits+", 2.2.5, 2.2.5", ", "))
		if got != c.want {
			t.Errorf("installed %s: got %q want %q", c.installed, got, c.want)
		}
	}
	if got := PickFixed("1.0.0", []string{"v1.4.0", "1.2.0", "abc", "1.3.0"}); got != "1.2.0" {
		t.Errorf("v prefix and ordering: %q", got)
	}
	if got := PickFixed("", []string{"1.1", "1.2", "1.3", "1.4"}); got != "1.1" && got != "1.1, 1.2, 1.3" {
		t.Errorf("at most three: %q", got)
	}
	if got := PickFixed("1.0", []string{commits}); got != "" {
		t.Errorf("hashes only: %q", got)
	}
	doc := `{"results":[{"source":{"path":"/r/go.mod"},"packages":[{"package":{"name":"x","version":"2.2.0","ecosystem":"Go"},
	 "vulnerabilities":[{"id":"GHSA-aaaa-bbbb-cccc","affected":[{"package":{"name":"x"},"ranges":[{"events":[{"introduced":"0"},
	 {"fixed":"70f906c51ce49c485f1d355703e9cc3386b1cc2b"},{"fixed":"afd63b16170b7c047f5758eb910c416511e9c965"},{"fixed":"2.2.5"}]}]}]}]}]}]}`
	leads, err := ParseOSV("/r", []byte(doc))
	if err != nil || len(leads) != 1 || leads[0].FixedVersion != "2.2.5" {
		t.Fatalf("%v %+v", err, leads)
	}
}
