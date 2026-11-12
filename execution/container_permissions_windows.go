package execution

// Docker Desktop shares Windows directories using its file-sharing service. The
// real self-test checks that the fixed container user can use that bind mount.
func prepareContainerRoots(Roots) error { return nil }
