package locale

func init() {
	for _, msg := range []string{
		"The pull request number is not valid",
		"The base of the reviewed change, %s, is not a branch of %s",
		"The pull request comes from another repository, so a fix cannot be published on top of its commits",
		"The reviewed head is not a branch of the repository, so a fix cannot be published on top of it",
		"The reviewed head is not the current tip of a branch of the repository, so a fix cannot be published on top of it",
		"A change needs a pull request number, or both a base and a head",
		"Give either a pull request or a base..head range, not both",
		"A range must look like base..head",
		"A change needs a base",
		"git fetch of the change from %s failed",
		"The base or the head of the change could not be resolved",
		"The base and the head of the change have no common ancestor in the fetched history",
		"The base and the head must be full commit ids",
		"Could not check out the head of the change",
		"git %s failed: %v: %s",
		"API token not found",
		"The token needs a name of at most 100 characters",
		"Choose at least one scope: reviews:read or reviews:write",
		"The expiry must be in the future",
		"Too many active API tokens; revoke one first",
		"usage: /code-review <owner/repo | owner/repo#123 | owner/repo base..head> [light|balanced|comprehensive]",
		"A change review names its change; leave the ref out",
		"Could not read pull request #%d of %s from GitHub. If the repository is private, add a GitHub token with read access to it under System settings",
		"GitHub did not return a usable base branch for pull request #%d",
		" This review is limited to one change, described in the review context. Report problems that the change introduces or exposes.",
		"GitHub %s %s: %d",
		"GitHub request failed: %v",
		"This API token cannot be used on this endpoint",
		"The API token does not have the %s scope",
		"expires_at must be an RFC 3339 time, for example 2027-01-31T00:00:00Z",
		"instructions are too long",
		"With a pull request, head must be a full commit id or empty",
		"only_change must be true or false",
		"Review not found",
	} {
		Register(msg, msg)
	}
}
