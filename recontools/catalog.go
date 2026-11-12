// Package recontools implements an optional one-click installer for a small
// set of ProjectDiscovery recon binaries (subfinder, httpx, naabu, dnsx).
//
// Counterweight itself never requires these: they are declared as regular "shell"
// tools (same convention as nmap/sqlmap/ffuf — a key+description appended to
// the Bash tool's prompt, no exec wiring) and used only when actually present.
// This package exists purely to remove the manual "go find a release, verify
// it, put it on PATH" step for an operator who wants them, following the same
// download/verify/install shape as the selfupdate package but against a
// different (third-party, per-tool) trust boundary — see github.go.
package recontools

// ToolSpec describes one optional recon tool this installer knows how to fetch.
type ToolSpec struct {
	// Key is the binary name, the Tools-catalog key, and (for all four
	// currently cataloged tools) the GitHub release asset's project name.
	Key string
	// Repo is the GitHub "owner/name" release source.
	Repo string
	// Description is used both in the installer UI and, on a fresh install,
	// as the auto-registered shell tool's model-facing description.
	Description string
}

// catalog is intentionally small and fixed: these are the four ProjectDiscovery
// CLIs the recon-planning skill names as optional enhancements. Adding a tool
// here only works if its GitHub release follows the same goreleaser layout
// (archives named "<project>_<version>_<os|macOS>_<arch>.zip" plus a checksum
// manifest) — true for all four as of this package's writing, not guaranteed
// for an arbitrary future addition without checking its .goreleaser.yml first.
var catalog = []ToolSpec{
	{
		Key:         "subfinder",
		Repo:        "projectdiscovery/subfinder",
		Description: "ProjectDiscovery subfinder: passive subdomain enumeration. Usage: subfinder -d <domain> -silent",
	},
	{
		Key:         "httpx",
		Repo:        "projectdiscovery/httpx",
		Description: "ProjectDiscovery httpx: fast HTTP probing (status code, title, tech fingerprint) over a host/URL list. Usage: httpx -l <file> -silent, or: echo <host> | httpx -silent",
	},
	{
		Key:         "naabu",
		Repo:        "projectdiscovery/naabu",
		Description: "ProjectDiscovery naabu: fast TCP port discovery. Usage: naabu -host <host> -silent",
	},
	{
		Key:         "dnsx",
		Repo:        "projectdiscovery/dnsx",
		Description: "ProjectDiscovery dnsx: fast, bulk DNS resolution and record lookup. Usage: dnsx -l <file> -silent, or: echo <host> | dnsx -silent",
	},
}

// Catalog returns the fixed set of installable recon tools.
func Catalog() []ToolSpec {
	out := make([]ToolSpec, len(catalog))
	copy(out, catalog)
	return out
}

// Find looks up a catalog entry by key.
func Find(key string) (ToolSpec, bool) {
	for _, t := range catalog {
		if t.Key == key {
			return t, true
		}
	}
	return ToolSpec{}, false
}
