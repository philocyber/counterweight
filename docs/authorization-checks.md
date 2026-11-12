# Authorization checks

`counterweight authz` checks that an application still enforces who may do what. You write the rules
once as a policy file: a list of requests and, for each one, what every kind of user must get back.
The command sends each request as each user and compares the answers with the policy.

No model is involved, so the same policy and the same target always give the same result. It runs in
two places, with the same policy format:

- **From the command line**, for a CI pipeline: a route that becomes reachable by the wrong user fails
  the job. This needs no Counterweight server and no database, only the binary, the policy file and
  the target. Credentials come from environment variables.
- **Inside a task, from the console** ([In a task](#in-a-task)): you store test accounts on the task,
  fill in a matrix of requests and roles (or paste the policy as text) and press Run. The result
  shows each role by name and goes into the task report.

It is a regression check, not a discovery tool. It tests the requests you list and nothing else.

## Set up in four steps

1. **Write a policy file** (see [The policy file](#the-policy-file)). Keep it in the repository of the
   application it describes.
2. **Check it.** `counterweight authz validate policy.yaml` reads the file, sends nothing, and lists the
   environment variables the run will need.
3. **Provide the credentials as environment variables**, from the secret store of your CI system. A
   credential is never written in the policy file; the file only names the variable.
4. **Run it** against a staging environment:

   ```bash
   counterweight authz run -o authz-report.json policy.yaml
   ```

From a source checkout, use `go run ./cmd/counterweight authz ...`.

### Exit status

| Status | Meaning |
|---|---|
| `0` | Every check was sent and passed. |
| `1` | The policy is violated: at least one check failed. |
| `2` | The run proves nothing: the policy is invalid, a variable is not set, a user could not sign in or prove its identity, a check was inconclusive, or no check was sent. |

Treat `2` as a broken job, never as a pass. When a run has both failed and inconclusive checks, the
status is `1`.

## In a task

Open a task that tests a running application and press **Test accounts** in its header, or choose
**Findings** and then **Authorization**. A code review task does not offer the tab: it has no running
application to check.

1. **Add test accounts.** Each one has a role name (for example "Admin tenant A"), a username, a
   password and, for an API without a login request, a fixed token. The role name gives the account
   its key, in lowercase with underscores (`admin_tenant_a`). The key is fixed once the account
   exists; the role name can be changed later. A task holds up to 20 accounts. You can also enter
   accounts (role name, username, password) in the **New task** form: they are stored together
   with the task. A switch there, off unless you turn it on, lets the agents test as those roles
   ([Letting the agents test as a role](#letting-the-agents-test-as-a-role)).
2. **Write the policy**, as a matrix or as text ([below](#the-matrix-and-the-text)). Saving checks the
   policy and lists every problem, including an account or a field that does not exist.
3. **Run check.** The run happens on the server. One run per task at a time; a run is stopped after
   30 minutes.
4. **Read the result.** A table of requests and roles, each role under its name, and the detail of
   every check that did not pass. The last 20 runs are kept. The task report (Markdown and PDF)
   shows the latest run as an "Authorization matrix" section.

```yaml
version: 1
target:
  base_url: https://staging.example.com

personas:
  anonymous: {}
  admin_tenant_a:
    headers:
      Authorization: "Bearer ${account:admin_tenant_a.token}"
    identity:
      request: { method: GET, path: /api/me }
      body_contains: ['"role":"admin"']
  tenant_b_user:
    login:
      request:
        method: POST
        path: /api/login
        json: { email: "${account:tenant_b_user.username}", password: "${account:tenant_b_user.password}" }
      token: { json_pointer: /token, prefix: "Bearer " }
    identity:
      request: { method: GET, path: /api/me }
      body_contains: ["${account:tenant_b_user.username}"]

cases:
  - id: admin-users
    request: { method: GET, path: /api/admin/users }
    expect:
      anonymous: { deny: 401 }
      tenant_b_user: { deny: 403 }
      admin_tenant_a: { allow: 200 }
```

### The matrix and the text

The policy has two views of the same thing. What you save is always the policy text, in the format
described under [The policy file](#the-policy-file).

**Matrix.** Requests down, roles across.

- **Address of the application**: the one address the check talks to.
- **Add role**: one of the task's test accounts, or a visitor who is not signed in. A role's menu
  opens **How it signs in**: a sign-in request (its path, JSON or form, the names of the username and
  password fields, and whether the answer gives a token or sets a cookie) or the account's stored
  token sent in a header. The same form holds the proof that a session belongs to the role: a page
  and a text only that role's page shows. It starts as the account's username, which a profile page
  usually shows. One sign-in can be applied to every role at once.
- **Add request**: a method and a path. In each cell, **Allow** or **Deny** and the status that says
  so. A request that changes data is sent only after you tick its confirmation. The details of a
  request hold its name, its JSON body, "no role may do this", and the texts an answer must or must
  not contain, which is how a denial is told from access when the status alone cannot.

**Text (YAML).** The same policy as text, for what the matrix does not show: several headers or
cookies for a role, form or raw bodies, comments. **Insert example** fills an empty editor with a
starting point built from your accounts. Name a persona after an account's key and take its
credentials with `${account:admin_tenant_a.username}`, `${account:admin_tenant_a.password}` and
`${account:admin_tenant_a.token}`; **Copy reference** next to an account gives you the three texts.

What to know when you switch between them:

- Saving from the matrix rewrites the text. Comments are not kept, and keys inside a JSON body come
  back in alphabetical order. Nothing a run sends or expects changes.
- A role whose sign-in was written in a way the form does not show keeps it; the matrix says so and
  leaves it to the text.
- After you change the text by hand, the matrix is closed until you save: only the server decides
  what a policy text means. A text the server cannot read as a policy, or one with a number too
  large for a browser to keep exact, is edited as text only.

How the stored accounts are protected:

- The password and the token are encrypted in the database with the server's credential key, like
  model API keys. The console can replace or remove them; it cannot read them back.
- They are never sent to a model. They are used by this check and, only for the accounts where you
  allow it, by a tool that lets the agents send requests as that role without seeing the secrets
  ([Letting the agents test as a role](#letting-the-agents-test-as-a-role)).
- A policy inside a task cannot read the server's environment: `${env:NAME}` is refused there, so a
  policy cannot send one of the server's own secrets to a target.
- The run obeys the blocked targets of the server and of the task, for the address in the policy and
  for the address it resolves to. A blocked target is reported when the policy is saved, and nothing
  is sent.
- The stored result holds no password and no token (see
  [What happens to credentials](#what-happens-to-credentials)). The task report shows statuses and
  the messages of the check, never an excerpt of a response.
- Accounts, policy and results are deleted with the task. Archiving a task deletes them too: they
  are not written into the archive package, so export the report first if you need the result.
- The endpoints need a console session. An API token for automation cannot reach them.

A password or token is refused when it starts or ends with a space or holds a line break: it would
not be sent exactly as stored. The policy text itself is stored as written, so do not type a secret
into it; use an account.

## The policy file

A complete, tested example is in
[`authz/testdata/example-policy.yaml`](../authz/testdata/example-policy.yaml). A short one:

```yaml
version: 1

target:
  base_url: ${env:AUTHZ_BASE_URL}        # for example https://staging.example.com

personas:
  anonymous: {}
  user_a:
    login:
      request:
        method: POST
        path: /api/login
        json: { email: a@tenant-a.test, password: "${env:AUTHZ_USER_A_PASSWORD}" }
      token: { json_pointer: /token, prefix: "Bearer " }
    identity:
      request: { method: GET, path: /api/me }
      body_contains: ['"email":"a@tenant-a.test"']
  admin:
    headers:
      Authorization: "Bearer ${env:AUTHZ_ADMIN_TOKEN}"
    identity:
      request: { method: GET, path: /api/me }
      body_contains: ['"role":"admin"']

cases:
  - id: admin-users
    request: { method: GET, path: /api/admin/users }
    expect:
      anonymous: { deny: 401 }
      user_a: { deny: 403 }
      admin: { allow: 200 }
```

A key the command does not know is an error, so a misspelled option cannot be silently ignored. The
file is a single YAML document; a second one after `---` is refused. A file that cannot be read as
YAML, or has an unknown key, is reported with that one error. For a file that can be read,
`validate` lists every problem at once.

### Target

| Key | Meaning |
|---|---|
| `base_url` | The only address the command talks to. Request paths are appended to it. It cannot hold a field of a test account, and its host name cannot end with a dot. |
| `allow_plain_http` | `true` accepts an `http://` address. Meant for a local fixture: over plain HTTP the credentials travel unencrypted. Default `false`. |
| `timeout_seconds` | Time limit of each request, 1 to 300. Default 15. |

### Personas

A persona is one identity the requests are sent as. Their order in the file is the order in the
report. An optional `role` gives the persona a name for the output ("Admin tenant A"); inside a task
the role name of the account with the same key is used unless the policy sets one.

- **Anonymous:** a persona with no content (`anonymous: {}`). Its requests carry no credential.
- **Fixed credential:** `headers` or `cookies`. A value is `${env:NAME}` and nothing else; a header
  may put one scheme word before it, as in `"Bearer ${env:NAME}"`.
- **Login:** `login` sends one request before the run. Cookies set by the answer are kept for the
  persona. With `token`, the value found at `json_pointer` in the JSON answer is sent in `header`
  (default `Authorization`) after `prefix` (default: none). `status` is the answer that means success
  (default 200). The body of the login request is `json`, `form` or `body`. A field or header of the
  login request whose name contains `pass`, `secret`, `token`, `key`, `credential`, `authorization` or
  `cookie` must take its value from the environment too.
- **Identity check:** every persona with a credential needs `identity`: a `GET` request and at least
  one `body_contains` text that only this persona's own profile shows, such as its email address.

The identity check runs before the first case and again after the last one:

- If a credential is expired, wrong or belongs to another user, the run stops before any case is sent
  (status `2`).
- If a session stops working during the run, the results of that persona become inconclusive, except
  access that was wrongly granted, which a dead session cannot explain.

### Cases

Each case is one request and the expected outcome for **every** persona. A case that leaves a persona
out is an error: the table has no implicit cells. A policy holds up to 200 cases and 20 personas.

| Key | Meaning |
|---|---|
| `id` | Name of the case in the report. Lowercase letters, digits, `_`, `-`, `.`. |
| `request` | `method` (`GET`, `HEAD`, `OPTIONS`, `POST`, `PUT`, `PATCH`, `DELETE`), `path` (starts with `/`), optional `headers`, and at most one of `json`, `form` and `body`. `Authorization`, `Cookie` and `Host` headers are refused here: credentials belong to the persona. |
| `expect.<persona>` | Exactly one of `allow` and `deny`, with the status or list of statuses that mean it. Optional `body_contains` and `body_not_contains` (lists of texts) and `location_contains` (a text of the `Location` header of a redirect). |
| `allow_write` | Must be `true` for `POST`, `PUT`, `PATCH` and `DELETE`. It confirms the request is safe to send to this target. |
| `deny_all` | Must be `true` for a case that no persona may reach. |

Rules that keep a wrong setup from looking like a pass:

- **A case needs an allowed persona.** If the owner cannot get through, the endpoint may be broken or
  the path mistyped, and then every denial would pass for the wrong reason. Such a case fails, and its
  denials are reported as inconclusive. The exception is a route nobody may reach, marked with
  `deny_all: true`.
- **`401`, `403` and `404` are different answers.** State the one your application gives. An
  application that hides a resource from other tenants with `404` is written `deny: 404`.
- **A denial that looks like access needs something that tells them apart.** That is a denial
  answered with a `2xx` status, or with a status an allowed persona of the same case also gets (a
  form that redirects with `302` on success and on refusal). Add one of these, or the file is
  rejected:
  - `body_contains`: a text only the refusal shows, such as the wording of the error page.
  - `location_contains`: where the refusal redirects to, such as `/login`.
  - `body_not_contains`: a value of the protected data, for an application that answers `200` with
    an empty list. Its absence counts only if an allowed persona of the same case did receive that
    text. Otherwise the text may simply be wrong, and the check is inconclusive; that is always the
    outcome for an answer without a body (`HEAD`, `204`). For the same reason the file is rejected
    when it is the only sign in a `deny_all` case.
- **Denied personas are sent first.** In a case that deletes a resource, the owner's allowed request
  goes last, so the denials are tried while the resource still exists.

### References

`${env:NAME}` is replaced with the value of the environment variable `NAME` when the run starts. It
can be used in `base_url`, in paths, headers, cookies, request bodies and expected texts. An unset or
empty variable stops the run. It is available on the command line only.

`${account:KEY.username}`, `${account:KEY.password}` and `${account:KEY.token}` are replaced with the
fields of the test account `KEY` of the task, in the same places except `base_url`. They are
available inside a task only.

A variable used under a persona's `headers`, `cookies` or `login` is a credential, and so are an
account's password and token wherever they are used. A credential is always removed from the output,
and the run stops if it starts or ends with whitespace or holds a control character (typically a
trailing newline in the stored secret): the value sent would not be the value stored. Any other
variable is removed from the output when it is 8 characters or longer, in case it is a key; shorter
values are taken as identifiers and left readable. An account's username is never removed: it names
the account.

## How a check is judged

Each pair of case and persona is one check, with one of three results.

| Result | Kind | When |
|---|---|---|
| pass | | The status is one of the expected ones and the content checks hold. |
| fail | `access_granted` | A denial was expected and the target granted access: a `2xx` status or a status that means access for this case, without the text or redirect that marks the refusal. |
| fail | `data_exposed` | The response contains a text listed in `body_not_contains`. |
| fail | `access_denied` | Access was expected and the target answered `401`, `403` or `404`. |
| fail | `content_missing` | The status matches but a `body_contains` text is absent. |
| fail | `unexpected_redirect` | The status matches but the redirect does not go to `location_contains`. |
| fail | `unexpected_status` | Any other status, for example `404` where the policy says `403`. |
| inconclusive | `unproven_marker` | The denial rests on `body_not_contains`, and no allowed persona received that text. |
| inconclusive | `server_error` | The target answered `429` or `5xx`, unless the policy expects exactly that. |
| inconclusive | `request_failed` | No answer: connection error, timeout, or a path that would leave the target. |
| inconclusive | `no_baseline` | The denial matched, but no allowed persona of the case got through. |
| inconclusive | `session_lost` | The persona's session stopped working during the run. |
| inconclusive | `truncated` | The response is larger than 1 MiB, so its content could not be checked in full. |

A redirect is never followed. A `302` to a login page is the answer that gets judged, so write
`deny: 302` if that is how your application denies, with `location_contains` when success redirects
too.

## The report

`-o` writes the report as JSON. The terminal output is a summary of the same data.

| Field | Content |
|---|---|
| `schema` | `counterweight-authz-report/1` |
| `policy_sha256` | Hash of the policy file that was run. |
| `target`, `started_at`, `duration_ms` | Where and when. |
| `result`, `exit_code` | `pass`, `fail`, `inconclusive` or `error`, and the exit status. |
| `counts` | Cases run, checks sent, and how many passed, failed or were inconclusive. A run that sent no check cannot pass. |
| `errors` | What stopped or weakened the run. |
| `personas` | Each persona, its role name when it has one, and whether its identity was verified. |
| `cases[].checks[]` | Persona, expectation, status received, result, kind, detail, duration, response size and, for a check that did not pass, an excerpt of the response. |

## What happens to credentials

- They are read from environment variables. The policy file is rejected if a persona's header or
  cookie holds any fixed text beyond a scheme word, or if a login field named like a credential holds
  a fixed value. The check goes by field name: a secret written under another name, or in a case
  request, is sent as written and is not masked. Keep every secret in a variable.
- They are sent to the host of `base_url` and nowhere else. A request path cannot name another host,
  and redirects are not followed.
- They do not pass through a proxy. Proxy settings of the environment are ignored, which includes
  Counterweight's own recording proxy: nothing of a run is stored as captured traffic.
- They are never sent to a model. The command does not use one.
- They are not written to the output. The report shows paths as written in the policy, with
  references unresolved, and a connection error is reported without the address that was
  requested. Messages and response excerpts have the run's own credentials removed: the values
  described under [References](#references), the tokens and cookies
  obtained at login, and their URL-encoded forms. In response excerpts, common credential shapes
  such as bearer tokens and JWTs are also masked, as in exported reports.

> [!IMPORTANT]
> The report still needs care. A check that did not pass keeps up to 300 bytes of the target's
> response, which for a real violation is data the persona should not have seen. Masking is based on
> exact values and known patterns: a credential the target returns in another encoding (for example
> Base64) is not recognised. Store the report as you would store a finding.

## In a CI pipeline

```yaml
name: Authorization check
on:
  workflow_dispatch:
  schedule:
    - cron: "0 5 * * *"

jobs:
  authz:
    runs-on: ubuntu-latest   # or a self-hosted runner that can reach staging
    steps:
      - uses: actions/checkout@v4
      # Put the counterweight binary on PATH here: from a release archive, or built from source.
      - run: counterweight authz run -o authz-report.json security/authz-policy.yaml
        env:
          AUTHZ_BASE_URL: ${{ vars.STAGING_URL }}
          AUTHZ_USER_A_PASSWORD: ${{ secrets.AUTHZ_USER_A_PASSWORD }}
          AUTHZ_ADMIN_TOKEN: ${{ secrets.AUTHZ_ADMIN_TOKEN }}
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: authz-report
          path: authz-report.json
```

- Use accounts that exist only for this check, in a staging environment, with the least privilege
  each persona needs.
- Anyone who can read the workflow's artifacts can read the report. Leave the upload step out if that
  is too wide for your repository.
- If you run it on pull requests: secrets are not available to workflows triggered from forks, so
  such a run stops with status `2`. Do not use `pull_request_target` to work around that.

## Writing the policy with the agents' help

During a web application assessment, the agents' authorization testing produces a ledger of endpoints
and of what each account received (skill `auth-matrix-testing`). When you ask for it, they can turn
that ledger into a draft policy: the personas and the requests, with every `expect` left empty and
what they observed written as a comment.

The expected outcomes are yours to fill in, from what the application is supposed to do. Do not copy
what was observed: if the application has an authorization flaw today, copying it turns the flaw into
the rule the check defends. A draft with an empty `expect` does not validate, so it cannot be run by
mistake.

## Letting the agents test as a role

The check above tests the requests you listed. The agents can go further and look for what you did
not list, such as another user's object reachable by changing an identifier. For that they need to
act as your test users, and they can do it without receiving the credentials.

It is off for every account. To turn it on, edit an account and switch on **Let the agents send
requests as this role**. When a task has at least one such account and a policy that validates, its
testing agents get a tool, `request_as_role`:

- The agent asks for the list of roles and the address of the target.
- The agent names a role, a method and a path on the target, with optional headers and a body.
- The server signs in with the stored account as the policy describes, checks the identity, sends
  the request and returns the status, the redirect target, the media type and up to 64 KiB of the
  body.

What keeps the credentials away from the model:

- The agent never receives a password, a token or a session. They are not in its prompt, not in
  what it sends to the tool, and not in what the approval gate is shown.
- The answer is cleaned before the agent sees it: the passwords and tokens of every account of the
  task, the tokens and cookies obtained at sign-in, in plain form and URL-encoded, JSON-escaped,
  HTML-escaped, in Base64 or in hexadecimal (all in lower or all in upper case), and common
  credential shapes such as bearer tokens, JWTs and JSON fields named like a credential.
- The agent chooses the method, the path, its own headers and the body. It cannot choose the host,
  cannot set `Authorization`, `Cookie` or `Host`, and a `${account:...}` it writes is sent as plain
  text. Redirects are not followed.
- Every request passes the approval gate and the tool rules with its real address, and the blocked
  targets of the server and of the task.
- A role is available only when every account its persona uses is switched on. The `anonymous`
  persona is available whenever the tool is.
- Switching an account off, or editing or deleting an account or saving the policy, ends the
  agents' signed-in sessions of the task at once, and a request that was waiting for approval is
  refused. A request already on its way to the target is not recalled. Sessions are kept for at
  most 10 minutes in any case, then the accounts sign in again; a deleted task's sessions go with
  that limit at the latest.
- These requests are not stored as captured traffic. The agent quotes the answer as evidence.

> [!IMPORTANT]
> Cleaning the answer cannot be complete. A target that returns a credential transformed in a way
> that is not listed above, or issues a new session in a field that does not look like one, shows
> it to the model. And an agent acting as a role can do whatever that role can do on the target,
> within the approval gate and your rules. Enable only accounts that exist for testing, on a
> staging environment, with the least privilege the role needs, and change their passwords when
> the engagement ends.

By default the tool is bound to the worker agents and to the single-agent pentest mode. It is never
given to code review agents or to a chat outside a task.

## Limits

- **Only what you list is tested.** A pass says the listed requests behave as stated. It says nothing
  about a route that is not in the file.
- **Resources must already exist.** The command does not create or clean up test data. A case names a
  fixed resource, for example a document of tenant A whose identifier comes from a variable.
- **A wrongly allowed write changes data.** If the target lets a denied persona delete a resource, it
  is deleted. Run against staging and against data you can lose.
- **Login is one HTTP request.** Sign-in flows that need a browser, several steps, a CSRF token read
  from a page, or a second factor are not supported; use a fixed token for those personas.
- **No import from an OpenAPI description.**
- **A failed check is not a finding.** Inside a task the result is shown in the Authorization tab and
  in the task report. It is not added to the findings, so it is not exported to Jira and has no
  retest.
- **Certificates are verified against the system trust store.** There is no option to skip
  verification.
- **One request per check, in order, without retries.** Each request opens its own connection.
- What has been verified is recorded in [VERIFICATION.md](VERIFICATION.md).
