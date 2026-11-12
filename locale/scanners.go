package locale

func init() {
	for _, m := range []string{
		"Dependency advisories from lockfiles",
		"Committed secrets in the working tree (git history is not scanned)",
		"%s was not found: no path is configured, it is not in the tools directory and it is not on PATH",
		"%s is missing from this container image, which should include it",
		"The image is outdated: rebuild it so it includes %s",
		"Download the %s release binary from %s and place it on PATH, or set its path in the scanner settings",
		" and %d more",
		" Resolving this lead resolves all %d advisories of the package.",
		"Scanner %s lists %d published advisories (%s) for package %s at version %s in %s. This only says the installed version is listed as affected, not that the code is exposed. Check by reading the code whether this code uses the affected functionality with untrusted input.%s",
		" A fixed version is %s.",
		"Verify the %d unverified automated leads delivered in the hints that mention a lead number: read the code for each, record a finding with report_finding only if the lead is a real issue, and resolve every lead with resolve_scan_lead.",
		"The review reached its end. %d unverified automated leads were handed over for verification.",
		"Unverified automated lead #%d (not a finding; it does not change the scope of this review). ",
		" Confirm or dismiss it by reading the code. If it is a real issue, record it with report_finding as usual, never writing a secret value into a finding. Then call resolve_scan_lead with lead_id=%d and status confirmed (with finding_id) or dismissed (with the reason).",
		"Scanner %s matched rule \"%s\" at %s, a possible committed secret. Read that location and judge whether it is a real credential or a placeholder, example or test value.",
		"Record the outcome of an unverified automated lead delivered in a hint, after reading the code. status=confirmed when the lead is a real issue (pass the finding_id returned by report_finding when you recorded one); status=dismissed when it is not, with the reason.",
		"The lead number from the hint",
		"confirmed or dismissed",
		"Optional: the finding_id returned by report_finding for a confirmed lead",
		"Why the lead is a real issue or not; required when dismissing",
		"Invalid arguments",
		"Unknown lead number for this task",
		"A dismissal needs a reason",
		"status must be confirmed or dismissed",
		"finding_id is not a finding of this task",
		"Could not record the outcome",
		"Secrets are searched in the working tree only; git history is not scanned (a shallow clone has none).",
		"Unknown scanner: %s",
		"The path for %s must be an absolute path to an existing executable file",
	} {
		Register(m, m)
	}
}
