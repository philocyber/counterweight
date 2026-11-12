package privatefs

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateOperationsRejectJunctionParent(t *testing.T) {
	outside := t.TempDir()
	path := filepath.Join(outside, "secret")
	if err := os.WriteFile(path, []byte("outside-preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotPermissions(t, path)
	junction := filepath.Join(t.TempDir(), "junction")
	command := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, output)
	}
	if data, err := ReadFile(filepath.Join(junction, "secret")); err == nil || len(data) != 0 {
		t.Fatal("junction read escaped")
	}
	if err := ProtectFile(filepath.Join(junction, "secret")); err == nil {
		t.Fatal("junction permission repair escaped")
	}
	f, err := Create(filepath.Join(junction, "new-secret"))
	if f != nil {
		f.Close()
	}
	if err == nil {
		t.Fatal("junction creation escaped")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "outside-preserved" || snapshotPermissions(t, path) != before {
		t.Fatal("junction target changed")
	}
	if _, err := os.Stat(filepath.Join(outside, "new-secret")); !os.IsNotExist(err) {
		t.Fatal("junction target received a file")
	}
}

func TestWindowsPinnedParentSurvivesDirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	live, retired := filepath.Join(root, "live"), filepath.Join(root, "retired")
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(live, "secret")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	parentBefore := snapshotPermissions(t, live)
	parent, name, err := openWindowsParent(path)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(parent)
	if err := os.Rename(live, retired); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	replacementBefore := snapshotPermissions(t, path)
	h, err := openRelativeWindows(parent, name, windows.GENERIC_READ, privatePathSharing, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(h), path)
	data, readErr := io.ReadAll(f)
	f.Close()
	if readErr != nil || string(data) != "original" {
		t.Fatalf("read followed replacement: %q %v", data, readErr)
	}
	h, err = openRelativeWindows(parent, name, windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC, privatePathSharing, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if err != nil {
		t.Fatal(err)
	}
	f = os.NewFile(uintptr(h), path)
	if err := protectAndTruncateOpenedFile(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	_, writeErr := f.Write([]byte("protected-original"))
	f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	sd, err := privateDescriptor(false)
	if err != nil {
		t.Fatal(err)
	}
	h, err = openRelativeWindows(parent, "created", windows.GENERIC_WRITE, 0, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE, sd)
	if err != nil {
		t.Fatal(err)
	}
	f = os.NewFile(uintptr(h), filepath.Join(live, "created"))
	assertPrivate(t, filepath.Join(retired, "created"))
	_, writeErr = f.Write([]byte("new-private"))
	f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if data, err := os.ReadFile(filepath.Join(retired, "secret")); err != nil || string(data) != "protected-original" {
		t.Fatalf("original object lost: %q %v", data, err)
	}
	assertPrivate(t, filepath.Join(retired, "secret"))
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" || snapshotPermissions(t, path) != replacementBefore {
		t.Fatal("replacement file changed")
	}
	if snapshotPermissions(t, retired) != parentBefore {
		t.Fatal("parent permissions changed")
	}
	if _, err := os.Stat(filepath.Join(live, "created")); !os.IsNotExist(err) {
		t.Fatal("private file appeared in replacement directory")
	}
}

func TestWindowsPrivateExtendedAndRelativePaths(t *testing.T) {
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{root, `\\?\` + root, relative} {
		path := filepath.Join(base, "private-unicode-café")
		f, err := CreateTruncate(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write([]byte("preserved"))
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := ProtectFile(path); err != nil {
			t.Fatal(err)
		}
		if data, err := ReadFile(path); err != nil || string(data) != "preserved" {
			t.Fatalf("supported path lost: %q %v", data, err)
		}
	}
}

func TestWindowsPrivateNestedDirectoriesUnderPublicParent(t *testing.T) {
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
	first := filepath.Join(parent, "new")
	second := filepath.Join(first, "deep")
	target := filepath.Join(second, "store")
	if err := ProtectDirectory(target); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second, target} {
		assertPrivate(t, path)
	}
	if snapshotPermissions(t, parent) != before {
		t.Fatal("public ancestor permissions changed")
	}
}
