//go:build !windows

package privatefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerCredentialModes(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_INSTALLER_PERMISSION_TEST") != "1" {
		t.Skip("requires copied installer and a Unix shell fixture")
	}
	source, err := os.ReadFile(os.Getenv("COUNTERWEIGHT_TEST_INSTALLER"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"docker", "local"} {
		for _, existing := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-new", true: "-existing"}[existing], func(t *testing.T) {
				root := t.TempDir()
				bin := filepath.Join(root, "bin")
				os.Mkdir(bin, 0755)
				os.WriteFile(filepath.Join(root, "install.sh"), source, 0600)
				os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0755)
				os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nexit 1\n"), 0755)
				os.WriteFile(filepath.Join(root, ".env.example"), []byte("POSTGRES_PASSWORD=placeholder\nANTHROPIC_API_KEY=\nUNCHANGED=fixture\n"), 0644)
				os.Mkdir(filepath.Join(root, "tls"), 0700)
				os.WriteFile(filepath.Join(root, "tls", "server.pem"), []byte("synthetic stub"), 0600)
				os.WriteFile(filepath.Join(root, "tls", "server-key.pem"), []byte("synthetic stub"), 0600)
				credential := ".env"
				input := "1\nsynthetic-password\nfixture|&key\n"
				if kind == "local" {
					credential = "config.json"
					input = "2\n1\n127.0.0.1\n5432\nfixture\nsynthetic-password\nfixture\ndisable\n"
				}
				path := filepath.Join(root, credential)
				if existing {
					os.WriteFile(path, []byte("legacy-secret"), 0644)
					os.Chmod(path, 0644)
				}
				cmd := exec.Command("bash", "-c", "umask 022; exec bash ./install.sh")
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
				cmd.Stdin = strings.NewReader(input)
				output, err := cmd.CombinedOutput()
				if kind == "docker" && err != nil {
					t.Fatalf("Docker stub installer failed: %v %s", err, output)
				}
				if kind == "local" && err == nil {
					t.Fatal("local fixture unexpectedly continued beyond Go stub")
				}
				assertPrivate(t, path)
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !existing || kind == "local" {
					if !strings.Contains(string(body), "synthetic-password") {
						t.Fatal("credential workflow did not retain supplied password")
					}
				}
				if kind == "docker" && existing && string(body) != "legacy-secret" {
					t.Fatal("existing environment credentials overwritten")
				}
				if kind == "docker" && !existing && (!strings.Contains(string(body), "ANTHROPIC_API_KEY=fixture|&key") || !strings.Contains(string(body), "UNCHANGED=fixture")) {
					t.Fatal("template or literal key changed")
				}
			})
		}
	}
}
