package privatefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsInstallerCredentialACLs(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_INSTALLER_PERMISSION_TEST") != "1" {
		t.Skip("requires explicitly selected installer and hidden Git Bash fixture")
	}
	installer := os.Getenv("COUNTERWEIGHT_TEST_INSTALLER")
	source, err := os.ReadFile(installer)
	if err != nil {
		t.Fatal(err)
	}
	helper, helperErr := os.ReadFile(filepath.Join(filepath.Dir(installer), "private-credential.ps1"))
	if helperErr != nil {
		t.Fatal(helperErr)
	}
	for _, kind := range []string{"docker", "local"} {
		for _, existing := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-new", true: "-existing"}[existing], func(t *testing.T) {
				root := t.TempDir()
				sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
				before := snapshotPermissions(t, root)
				write := func(path, body string) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
					t.Fatal(err)
				}
				write("install.sh", string(source))
				write("private-credential.ps1", string(helper))
				write("bin/docker", "#!/bin/sh\nexit 0\n")
				write("bin/go", "#!/bin/sh\nexit 1\n")
				write(".env.example", "POSTGRES_PASSWORD=placeholder\nANTHROPIC_API_KEY=\nUNCHANGED=fixture\n")
				if err := os.Mkdir(filepath.Join(root, "tls"), 0700); err != nil {
					t.Fatal(err)
				}
				write("tls/server.pem", "synthetic stub")
				write("tls/server-key.pem", "synthetic stub")
				credential, input := ".env", "1\nsynthetic-password\nfixture|&key\n"
				if kind == "local" {
					credential, input = "config.json", "2\n1\n127.0.0.1\n5432\nfixture\nsynthetic-password\nfixture\ndisable\n"
				}
				if existing {
					write(credential, "legacy-secret")
				}
				cmd := exec.Command(os.Getenv("COUNTERWEIGHT_TEST_BASH"), "-c", `export PATH="$PWD/bin:$PATH"; umask 022; exec bash ./install.sh`)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				cmd.Dir = root
				cmd.Stdin = strings.NewReader(input)
				output, err := cmd.CombinedOutput()
				if kind == "docker" && err != nil {
					t.Fatalf("stub installer: %v %s", err, output)
				}
				if kind == "local" && err == nil {
					t.Fatal("continued beyond Go stub")
				}
				path := filepath.Join(root, credential)
				assertPrivate(t, path)
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if existing && kind == "docker" {
					if string(body) != "legacy-secret" {
						t.Fatal("existing credentials overwritten")
					}
				} else if !strings.Contains(string(body), "synthetic-password") {
					t.Fatal("password lost")
				}
				if kind == "docker" && !existing && (!strings.Contains(string(body), "ANTHROPIC_API_KEY=fixture|&key") || !strings.Contains(string(body), "UNCHANGED=fixture")) {
					t.Fatal("template or literal key changed")
				}
				if snapshotPermissions(t, root) != before {
					t.Fatal("parent ACL changed")
				}
				if _, err := os.Stat(filepath.Join(root, ".env.bak")); !os.IsNotExist(err) {
					t.Fatal("credential backup retained")
				}
			})
		}
	}
}

func TestWindowsCredentialHelperRejectsIndirectDestinations(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_INSTALLER_PERMISSION_TEST") != "1" {
		t.Skip("requires explicitly selected native PowerShell helper")
	}
	helper := filepath.Join(filepath.Dir(os.Getenv("COUNTERWEIGHT_TEST_INSTALLER")), "private-credential.ps1")
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("original-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotPermissions(t, outside)
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, link, filepath.Join(root, "missing-parent", "config.json")} {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-File", helper, "-Path", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("indirect/unsupported destination accepted: %s %s", path, output)
		}
	}
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != "original-fixture" || snapshotPermissions(t, outside) != before {
		t.Fatal("rejected link changed outside target")
	}
}

func TestWindowsCredentialTemplatePreservesExistingBytes(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_INSTALLER_PERMISSION_TEST") != "1" {
		t.Skip("requires explicitly selected native PowerShell helper")
	}
	helper := filepath.Join(filepath.Dir(os.Getenv("COUNTERWEIGHT_TEST_INSTALLER")), "private-credential.ps1")
	root := t.TempDir()
	template := filepath.Join(root, "template.json")
	path := filepath.Join(root, "config.json")
	original := "{\"password\":\"synthetic-café|&\"}\n"
	if err := os.WriteFile(template, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-File", helper, "-Path", path, "-Template", template)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("helper failed: %v %s", err, output)
		}
		assertPrivate(t, path)
		body, err := os.ReadFile(path)
		if err != nil || string(body) != original {
			t.Fatal("credential bytes changed")
		}
	}
	run()
	if err := os.WriteFile(template, []byte("different template"), 0600); err != nil {
		t.Fatal(err)
	}
	run()
}
