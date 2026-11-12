package traffic

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTrafficArchivePrivateFilesUnderPublicWindowsParent(t *testing.T) {
	parent := t.TempDir()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"SY": true, "BA": true, user.User.Sid.String(): true}
	check := func(path string) {
		t.Helper()
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		aces := regexp.MustCompile(`\([^()]*\)`).FindAllString(sd.String(), -1)
		if len(aces) != 3 {
			t.Fatalf("public archive ACL: %s", sd.String())
		}
		for _, ace := range aces {
			fields := regexp.MustCompile(`^\(A;[^;]*;FA;;;([^;]+)\)$`).FindStringSubmatch(ace)
			if len(fields) != 2 || !allowed[fields[1]] {
				t.Fatalf("public archive ACL: %s", sd.String())
			}
		}
	}
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if _, err := tr.ExportHosts([]string{"fixture.example"}, parent); err != nil {
		t.Fatal(err)
	}
	check(filepath.Join(parent, "traffic.json"))
	source, destination := filepath.Join(parent, "source.bin"), filepath.Join(parent, "copied.bin")
	if err := os.WriteFile(source, []byte("raw-private-body"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyArchiveBlob(source, destination); err != nil {
		t.Fatal(err)
	}
	check(destination)
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "raw-private-body" {
		t.Fatalf("body copy changed: %v", err)
	}
}
