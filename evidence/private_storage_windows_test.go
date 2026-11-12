package evidence

import (
	"regexp"
	"testing"

	"golang.org/x/sys/windows"
)

func setEvidenceEveryoneACL(t *testing.T, path, flags string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;WD)")
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
func makeEvidenceParentPublic(t *testing.T, path string) { setEvidenceEveryoneACL(t, path, "OICI") }
func makeEvidenceFilePublic(t *testing.T, path string)   { setEvidenceEveryoneACL(t, path, "") }
func evidencePermissions(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}
func assertEvidencePrivate(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"SY": true, "BA": true, user.User.Sid.String(): true}
	acl := evidencePermissions(t, path)
	aces := regexp.MustCompile(`\([^()]*\)`).FindAllString(acl, -1)
	if len(aces) != 3 {
		t.Fatalf("public evidence ACL: %s", acl)
	}
	for _, ace := range aces {
		fields := regexp.MustCompile(`^\(A;[^;]*;FA;;;([^;]+)\)$`).FindStringSubmatch(ace)
		if len(fields) != 2 || !allowed[fields[1]] {
			t.Fatalf("public evidence ACL: %s", acl)
		}
	}
}
