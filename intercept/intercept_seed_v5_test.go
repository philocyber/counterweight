package intercept

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

// No commands in this file are executed. Both historical and refined rules go
// through Match, including canonical shell subjects and the unmatched ask gate.
func TestInterceptSeedV5(t *testing.T) {
	type fixture struct {
		tool, input string
	}
	var all []fixture
	exceptions := make(map[fixture]string)
	add := func(tool, key, value string) fixture {
		var input bytes.Buffer
		encoder := json.NewEncoder(&input)
		encoder.SetEscapeHTML(false) // Valid raw JSON tool calls can contain literal redirects.
		if err := encoder.Encode(map[string]string{key: value}); err != nil {
			t.Fatal(err)
		}
		f := fixture{tool, strings.TrimSpace(input.String())}
		all = append(all, f)
		return f
	}
	match := func(name, pattern string, f fixture) bool {
		r := semanticFixture(pattern, "deny")
		r.Name, r.ID = name, 505
		i := &Interceptor{cached: []compiledRule{r}}
		dec, hit := i.Match(f.tool, []byte(f.input))
		if hit && dec.RuleID == r.ID && dec.RuleName == name && dec.Action == "deny" {
			return true
		}
		if f.tool == "Bash" || f.tool == "shell_send" {
			if !hit || dec.Action != "ask" || dec.RuleID == r.ID {
				t.Errorf("missing independent shell review for %s: %+v, hit=%v", f.input, dec, hit)
			}
		} else if hit {
			t.Errorf("unexpected non-shell decision for %s: %+v", f.input, dec)
		}
		return false
	}
	commands := map[string][]string{
		"[Built-in] Recursive forced deletion: rm -rf":  {"rm -rf /tmp/fixture", "rm -fr /tmp/fixture", "rm --recursive /tmp/fixture", "rm --no-preserve-root /"},
		"[Built-in] Delete critical system directories": {"rm /", "rm /etc", "rm /bin/", "rm /usr/*", "rm /boot", "rm /var", "rm /lib", "rm /sys", "rm /proc", "rm /dev", "rm /sbin", "rm /root", "rm '/etc/'", `rm "/etc"`, "rm /etc*"},
		"[Built-in] Disk formatting: mkfs":              {"mkfs /dev/sdb", "mkfs.ext4 /dev/sdb"},
		"[Built-in] Overwrite disk devices: dd":         {"dd if=/dev/zero of=/dev/sdb", "dd of=/dev/nvme1n1 if=image"},
		"[Built-in] Shutdown / reboot":                  {"shutdown now", "reboot", "halt", "poweroff", "init 0", "init 6"},
		"[Built-in] Kill all processes":                 {"kill -9 -1", "killall -9 worker"},
		"[Built-in] Disk wiping: shred / wipe":          {"shred /dev/sdb", "wipe /dev/sdb"},
		"[Built-in] Flush firewall rules":               {"iptables -F", "iptables --flush", "nft flush ruleset"},
		// Preserve V2's residual signatures, including its historical word
		// boundary before ':' and '>'. V1 covers the ordinary fork-bomb form.
		"[Built-in] Destructive system commands": {"echo x>/dev/sdb", "x:(){ echo fixture; }", "dd if=image > /dev/sdb", "echo fixture > /dev/nvme1n1"},
	}
	benign := map[string][]string{
		"[Built-in] Recursive forced deletion: rm -rf":  {"cat /tmp/rm-notes -rf", "echo --rm -rf", "echo pre_rm -rf", "rm one.tmp"},
		"[Built-in] Delete critical system directories": {"rm /tmp/fixture", "rm /etc-backup", "rm /usr/local/fixture", "rm /rooted", "rm '/etc/fixture'", `rm "/var/tmp/fixture"`, "rm /etc/fixture*", "cat /tmp/rm-notes /etc"},
		"[Built-in] Disk formatting: mkfs":              {"cat /tmp/mkfs-notes", "echo --mkfs", "echo pre_mkfs"},
		"[Built-in] Overwrite disk devices: dd":         {"dd if=/dev/sdb of=backup.img", "dd if=input of=copy", "echo --dd of=/dev/sdb", "cat /tmp/dd-notes of=/dev/sdb"},
		"[Built-in] Shutdown / reboot":                  {"curl https://example.test/reboot", "cat /tmp/reboot.log", "echo --reboot", "echo pre_reboot", "init 3"},
		"[Built-in] Kill all processes":                 {"kill -0 123", "echo --kill -9 -1", "cat /tmp/killall-notes -9"},
		"[Built-in] Disk wiping: shred / wipe":          {"shred /tmp/fixture", "echo --wipe /dev/sdb", "cat /tmp/shred-notes /dev/sdb"},
		"[Built-in] Flush firewall rules":               {"iptables -L", "nft list ruleset", "echo --iptables -F", "cat /tmp/nft-notes flush ruleset"},
		"[Built-in] Destructive system commands":        {"dd if=/dev/sdb of=backup.img", "dd if=input of=copy", "cat /dev/sdb", "echo fixture >out.txt", "curl https://example.test/reboot", "cat /tmp/mkfs-notes"},
	}
	for _, r := range db.InterceptRuleRefinementsV5() {
		t.Run(r.Name, func(t *testing.T) {
			if len(commands[r.Name]) == 0 || len(benign[r.Name]) == 0 {
				t.Fatal("missing fixtures")
			}
			for _, command := range commands[r.Name] {
				forms := []string{command}
				if r.Name != "[Built-in] Destructive system commands" {
					program, args, spaced := strings.Cut(command, " ")
					obfuscated := program[:1] + "'" + program[1:] + "'"
					if spaced {
						obfuscated += " " + args
					}
					forms = append(forms, obfuscated)
					for _, wrapper := range []string{"sudo -n -u root ", "env -i MODE=fixture ", "nohup ", "timeout -s TERM 2s ", "nice -n 5 ", "command -p ", "exec -a fixture ", "sudo env -i nohup timeout 2s "} {
						forms = append(forms, wrapper+obfuscated)
					}
				}
				if r.Name == "[Built-in] Kill all processes" && strings.HasPrefix(command, "kill ") {
					forms = append(forms, "builtin "+command)
				}
				shellQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
				quoted := shellQuote(command)
				forms = append(forms, "bash -lc "+quoted, "bash -c "+fmt.Sprintf("%q", "sh -c "+quoted), "python3 -c "+shellQuote("import os; os.system("+strconv.Quote(command)+")"), "ssh host "+quoted, "find /tmp -exec "+command+` \;`, "printf x | xargs "+command, "true; "+command, "true\n"+command)
				if r.Name != "[Built-in] Destructive system commands" {
					forms = append(forms, "ssh host "+shellQuote("/opt/tools/"+command), "ssh host "+shellQuote("true&&"+command), "find /tmp -exec /opt/tools/"+command+` \;`, "printf x | xargs /opt/tools/"+command)
				}
				for _, form := range forms {
					for _, tool := range []string{"Bash", "shell_send"} {
						key := "command"
						if tool == "shell_send" {
							key = "text"
						}
						f := add(tool, key, form)
						if r.Name == "[Built-in] Destructive system commands" && strings.Contains(command, "> /dev/") {
							exceptions[f] = "Disk redirects with whitespace or non-sd disk names must remain covered after removing the broad dd if= trigger; the old redirect alternative missed them."
						}
						if r.Name == "[Built-in] Delete critical system directories" &&
							(strings.HasPrefix(form, "python3 -c ") || strings.HasPrefix(form, "ssh host ")) &&
							(strings.ContainsAny(command, `"'`)) {
							exceptions[f] = "Critical paths quoted inside interpreter/remote programs now survive nested JSON/shell quoting; the old quote-excluding argument scan missed these destructive calls."
						}
						if !match(r.Name, r.NewPattern, f) {
							t.Errorf("destructive fixture escaped: %s %s", tool, f.input)
						}
					}
				}
			}
			for _, command := range benign[r.Name] {
				for _, tool := range []string{"Bash", "WebFetch"} {
					key := "command"
					if tool == "WebFetch" {
						key = "url"
					}
					f := add(tool, key, command)
					if match(r.Name, r.NewPattern, f) {
						t.Errorf("false positive: %s %s", tool, f.input)
					}
				}
			}
			// URL delimiters, option names and identifiers cannot begin a program.
			for _, word := range []string{"rm", "mkfs", "dd", "shutdown", "reboot", "halt", "poweroff", "kill", "killall", "shred", "wipe", "iptables", "nft"} {
				for _, prefix := range []string{"https://", "https://example.test/", "https://example.test/?", "https://example.test/?q=", "https://example.test/#", "https://example.test/@", "https://example.test/a-"} {
					url := prefix + word
					for _, f := range []fixture{add("Bash", "command", "curl "+url), add("WebFetch", "url", url)} {
						if match(r.Name, r.NewPattern, f) {
							t.Errorf("URL false positive: %s", f.input)
						}
					}
				}
			}
		})
	}
	// V2 intentionally stops duplicating V1. Check that the complete refined
	// policy still denies those operations, including dd writes and fork bombs.
	var combined []compiledRule
	for _, r := range db.InterceptRuleRefinementsV5() {
		compiled := semanticFixture(r.NewPattern, "deny")
		compiled.Name = r.Name
		combined = append(combined, compiled)
	}
	combined = append(combined, semanticFixture(`:\(\)\s*\{[^}]*:\|:`, "deny"))
	for _, command := range []string{"rm -rf /", "mkfs.ext4 /dev/sdb", "dd if=image of=/dev/sdb", "dd if=image > /dev/nvme1n1", "shutdown now", "reboot", ":(){ :|:& };:"} {
		f := add("Bash", "command", command)
		i := &Interceptor{cached: combined}
		if dec, hit := i.Match(f.tool, []byte(f.input)); !hit || dec.Action != "deny" {
			t.Errorf("combined policy lost destructive operation %q: %+v", command, dec)
		}
	}
	// Cross product: every refinement against every fixture, through real Match.
	// Only the explicitly identified nested-path quoting and disk-redirection
	// gaps above may expand. Every other new match must match the old pattern.
	for _, r := range db.InterceptRuleRefinementsV5() {
		for _, f := range all {
			if match(r.Name, r.NewPattern, f) && !match(r.Name, r.OldPattern, f) {
				if (r.Name != "[Built-in] Delete critical system directories" && r.Name != "[Built-in] Destructive system commands") || exceptions[f] == "" {
					t.Errorf("unlisted differential expansion for %s: %s %s", r.Name, f.tool, f.input)
				}
			}
		}
	}
}
