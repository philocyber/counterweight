package recontools

import (
	"os"
	"os/exec"
)

// Status reports one catalog tool's current availability for the UI.
type Status struct {
	Key         string `json:"key"`
	Repo        string `json:"repo"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
	// Managed is true when the binary lives in Counterweight's own managed tools
	// directory (installed by this package); false when found elsewhere on
	// PATH (an operator installed it themselves — "use if installed" applies
	// either way, this just distinguishes who to credit/uninstall).
	Managed bool   `json:"managed"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
}

// StatusAll reports status for every cataloged tool.
func StatusAll(dataDir string) []Status {
	out := make([]Status, 0, len(catalog))
	for _, sp := range catalog {
		out = append(out, statusOne(dataDir, sp))
	}
	return out
}

func statusOne(dataDir string, sp ToolSpec) Status {
	st := Status{Key: sp.Key, Repo: sp.Repo, Description: sp.Description}
	managedPath := BinPath(dataDir, sp.Key)
	if info, err := os.Stat(managedPath); err == nil && !info.IsDir() {
		st.Installed = true
		st.Managed = true
		st.Path = managedPath
		st.Version = probeVersion(managedPath)
		return st
	}
	if p, err := exec.LookPath(sp.Key); err == nil {
		st.Installed = true
		st.Managed = false
		st.Path = p
		st.Version = probeVersion(p)
	}
	return st
}
