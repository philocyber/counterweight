//go:build !linux && !windows

package execution

// Docker Desktop translates native file permissions. Readiness is determined by
// an actual write/read round trip with the unprivileged container user.
func prepareContainerRoots(Roots) error { return nil }
