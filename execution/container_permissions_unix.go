//go:build linux

package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// A per-user/default ACL lets the fixed worker UID access task files without
// changing ownership or making private workspaces world-readable/writable.
func prepareContainerRoots(roots Roots) error {
	if os.Getuid() == 65532 {
		return errors.New("The server must not use sandbox UID 65532")
	}
	path, err := exec.LookPath("setfacl")
	if err != nil {
		return errors.New("Container workspace access needs POSIX ACL support: install the acl package (setfacl), then recheck")
	}
	write := map[string]bool{}
	for _, p := range roots.Write {
		write[p] = true
	}
	seen := map[string]bool{}
	for _, p := range append(append([]string{}, roots.Read...), roots.Write...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		access := "r-X"
		if write[p] {
			access = "rwX"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = exec.CommandContext(ctx, path, "-R", "-P", "-m", "u:65532:"+access, "--", p).Run()
		if err == nil && write[p] {
			// Both directions need access to newly created files. Default ACLs
			// apply to directories only; never follow repository symlinks.
			entry := "d:u:65532:rwx,d:u:" + strconv.Itoa(os.Getuid()) + ":rwx"
			err = filepath.WalkDir(p, func(dir string, info os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !info.IsDir() {
					return nil
				}
				return exec.CommandContext(ctx, path, "-m", entry, "--", dir).Run()
			})
		}
		cancel()
		if err != nil {
			return errors.New("Container workspace ACL setup failed; enable POSIX ACLs for the data directory and recheck")
		}
	}
	return nil
}
