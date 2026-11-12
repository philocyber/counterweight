package config

import (
	"regexp"
	"testing"

	"golang.org/x/sys/windows"
)

func configPermissions(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

func assertConfigPrivate(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"SY": true, "BA": true, user.User.Sid.String(): true}
	acl := configPermissions(t, path)
	aces := regexp.MustCompile(`\([^()]*\)`).FindAllString(acl, -1)
	if len(aces) != 3 {
		t.Fatalf("unexpected config ACL: %s", acl)
	}
	for _, ace := range aces {
		fields := regexp.MustCompile(`^\(A;[^;]*;FA;;;([^;]+)\)$`).FindStringSubmatch(ace)
		if len(fields) != 2 || !allowed[fields[1]] {
			t.Fatalf("unexpected config access: %s", acl)
		}
	}
}
