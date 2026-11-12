package agent

import (
	"os"
	"strings"
)

// These server-owned credentials are not implicit task credentials. Ordinary
// tool environment, proxy routing and explicit TOOL_* data channels are retained.
var executorCredentialKeys = []string{
	"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
	"COUNTERWEIGHT_PG_DSN", "COUNTERWEIGHT_LLM_PROXY", "PGPASSWORD", "POSTGRES_PASSWORD",
}

// The SDK's Grep would otherwise run a global npm install of ripgrep the first
// time it is used on a host without rg. With installation off it still
// searches: with an rg already on PATH, or with its built-in implementation.
func init() {
	if os.Getenv("NORMA_RIPGREP_NO_INSTALL") == "" {
		_ = os.Setenv("NORMA_RIPGREP_NO_INSTALL", "1")
	}
}

// ExecutorEnvironmentOverrides masks inherited SDK credentials without changing
// the parent process. Caller overrides cannot restore a server credential key.
func ExecutorEnvironmentOverrides(extra []string) []string {
	blocked := make(map[string]bool, len(executorCredentialKeys))
	for _, key := range executorCredentialKeys {
		blocked[key] = true
	}
	filtered := make([]string, 0, len(extra)+len(blocked))
	for _, entry := range extra {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[strings.ToUpper(key)] {
			filtered = append(filtered, entry)
		}
	}
	for _, key := range executorCredentialKeys {
		filtered = append(filtered, key+"=")
	}
	return filtered
}

// ExecutorEnvironment supplies the same filtered environment to native custom
// processes. Empty credentials are intentional masks over inherited values.
func ExecutorEnvironment(extra []string) []string {
	return append(os.Environ(), ExecutorEnvironmentOverrides(extra)...)
}
