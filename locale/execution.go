package locale

func init() {
	Register("working directory escapes the data directory", "working directory escapes the data directory")
	Register("working directory could not be created or opened: %w", "working directory could not be created or opened: %w")
	Register("file root could not be opened: %w", "file root could not be opened: %w")
	Register("file root escapes the data directory", "file root escapes the data directory")
	Register("file root is not a directory", "file root is not a directory")
	Register("Path is outside the task's files or is unavailable within its allowed roots.", "Path is outside the task's files or is unavailable within its allowed roots.")
	Register("Task paused: file confinement is unavailable. Recheck execution isolation and the task's file roots before resuming.", "Task paused: file confinement is unavailable. Recheck execution isolation and the task's file roots before resuming.")

	Register("Mixed execution modes; some segments ran with host file access; commands and network access ran on the host.", "Mixed execution modes; some segments ran with host file access; commands and network access ran on the host.")
	Register("File tool access confined to task roots; commands, extensions and network access run on the host.", "File tool access confined to task roots; commands, extensions and network access run on the host.")
	Register("Host execution assumed (no execution record); files, commands and network access were not isolated by this layer.", "Host execution assumed (no execution record); files, commands and network access were not isolated by this layer.")
	Register("Host execution; files, commands and network access are not isolated by this layer.", "Host execution; files, commands and network access are not isolated by this layer.")
	Register("File confinement self-test has not passed", "File confinement self-test has not passed")
	Register("Symbolic link read/write probes skipped: platform could not create a symbolic link", "Symbolic link read/write probes skipped: platform could not create a symbolic link")
	Register("File confinement self-test could not create its fixture", "File confinement self-test could not create its fixture")
	Register("File confinement self-test could not create its workspace", "File confinement self-test could not create its workspace")
	Register("File confinement self-test could not plant its fixture", "File confinement self-test could not plant its fixture")
	Register("File confinement self-test failed: read escape route %d", "File confinement self-test failed: read escape route %d")
	Register("File confinement self-test failed: write escape route %d", "File confinement self-test failed: write escape route %d")
	Register("File confinement self-test failed: outside marker changed", "File confinement self-test failed: outside marker changed")
	Register("File confinement self-test failed: inside write", "File confinement self-test failed: inside write")
	Register("File confinement self-test failed: inside read", "File confinement self-test failed: inside read")
}
