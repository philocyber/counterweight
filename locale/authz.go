package locale

// Messages of the authorization check of a task (db/task_authz.go, server/task_authz.go).
func init() {
	for _, msg := range []string{
		"Task not found",
		"Test account not found",
		"Authorization run not found",
		"The account needs a role name of at most 80 characters that contains a letter or a digit",
		"Another test account of this task has the same key; choose a different role name",
		"This task already has the maximum number of test accounts",
		"The username, password or token is too long",
		"A password or token must not start or end with a space and must not contain line breaks or other control characters",
		"The policy is larger than 256 KiB",
		"An authorization check tests a running application; a code review task has none",
		"The authorization check data could not be read or saved",
		"${env:%s}: a policy inside a task cannot read the server's environment; store the value in a test account and write ${account:KEY.password}",
		"${account:%s.%s}: this task has no test account with the key %s",
		"${account:%s.%s}: the test account %s has no %s",
		"The blocked-target rules are not available, so the run cannot be checked against them",
		"The request is not a valid test account",
		"The request needs the policy text",
		"Save a policy before starting a run",
		"The policy has problems; fix them before starting a run",
		"An authorization run is already in progress for this task",
		// request_as_role (server/task_authz_agent.go)
		"No test account of this task is enabled for agents",
		"This task has no authorization policy yet; the operator has to save one before roles can be used",
		"The task's authorization policy has problems the operator has to fix: %s",
		"request_as_role works inside a task only",
		"The parameters of request_as_role are not valid; headers and form take text values",
		"Role %s is not available to agents in this task. Available roles: %s",
		"Send one HTTP request to the application under test as one of this task's stored test roles, to test authentication and authorization (access control, IDOR, privilege escalation) without handling credentials. The server signs in with the stored account and adds the session; you never receive the password, token or cookie, and they are removed from the answer.\n",
		"Call it with no method and no path to list the available roles and the target address.\n",
		"To send a request give role, method and path (a path on the target that starts with /), optionally headers, and at most one of json, form or body. You cannot choose another host and cannot set Authorization, Cookie or Host headers. Redirects are not followed: a 3xx answer is returned with its Location.\n",
		"Send the same request as different roles to compare what each one gets. These requests are not stored as captured traffic, so quote the returned status and body as evidence when you report a finding.",
		"Role key from the role list, for example anonymous or admin_tenant_a",
		"GET, HEAD, OPTIONS, POST, PUT, PATCH or DELETE",
		"Path on the target, starting with /; may carry a query string",
		"Extra request headers, as an object of text values",
		"Request body sent as JSON",
		"Request body sent as a URL-encoded form, as an object of text values",
		"Request body sent as written",
	} {
		Register(msg, msg)
	}
}
