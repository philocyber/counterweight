package privatefs

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"golang.org/x/sys/windows"
)

func snapshotPermissions(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}
func assertPrivate(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := snapshotPermissions(t, path)
	aces := regexp.MustCompile(`\([^()]*\)`).FindAllString(sddl, -1)
	if len(aces) != 3 {
		t.Fatalf("private ACL has unexpected principals: %s", sddl)
	}
	allowed := map[string]bool{"SY": true, "BA": true, user.User.Sid.String(): true}
	for _, ace := range aces {
		matches := regexp.MustCompile(`^\(A;[^;]*;FA;;;([^;]+)\)$`).FindStringSubmatch(ace)
		if len(matches) != 2 || !allowed[matches[1]] {
			t.Fatalf("unexpected access grant: %s", sddl)
		}
	}
}

func TestProtectTreeRemovesExplicitEveryoneGrant(t *testing.T) {
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
	if err := ProtectTree(root); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, root)
}

func TestPrivateFileExcludesBroadParentAndRepairsExistingGrant(t *testing.T) {
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
	before := snapshotPermissions(t, parent)
	path := filepath.Join(parent, "secret")
	f, err := createPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("legacy-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(path); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path)
	if snapshotPermissions(t, parent) != before {
		t.Fatal("broad parent permissions were changed")
	}
}

func TestNativePermissionRepairWithoutDataAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-secret")
	if err := os.WriteFile(path, []byte("preserved-credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Permit metadata/synchronization/security repair, but no data read/write.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;0x160080;;;" + user.User.Sid.String() + ")")
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
	t.Cleanup(func() {
		if private, err := privateDescriptor(false); err == nil {
			if acl, _, err := private.DACL(); err == nil {
				_ = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
			}
		}
	})
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("fixture unexpectedly grants data access before permission repair")
	}
	if err := ProtectFile(path); err != nil {
		t.Fatalf("owner cannot repair a data-inaccessible legacy file: %v", err)
	}
	assertPrivate(t, path)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserved-credentials" {
		t.Fatalf("credential data not restored: %q %v", data, err)
	}
}
