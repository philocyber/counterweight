package traffic

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func makePublicTrafficTestPath(t *testing.T, path string) {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func assertTrafficPrivateArtifact(t *testing.T, path string, directory, _ bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.IsDir() != directory {
		t.Fatalf("invalid artifact: %s: %v", path, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"SY": true, "BA": true, user.User.Sid.String(): true}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	aces := regexp.MustCompile(`\([^()]*\)`).FindAllString(sd.String(), -1)
	if len(aces) != 3 {
		t.Fatalf("unexpected private DACL: %s: %s", path, sd.String())
	}
	for _, ace := range aces {
		fields := strings.Split(strings.Trim(ace, "()"), ";")
		if len(fields) != 6 || fields[0] != "A" || !allowed[fields[5]] {
			t.Fatalf("unrelated principal in private DACL: %s: %s", path, sd.String())
		}
	}
}

func assertTrafficUnrelatedRead(t *testing.T, _ string, _ bool) {
	t.Helper()
	// Windows checks the effective DACL above; this fixture does not impersonate
	// another account or change the host's local users.
}
