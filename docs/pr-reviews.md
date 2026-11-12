# Reviewing pull requests

Counterweight can review the code of a pull request from a GitHub Actions workflow. The workflow asks your
Counterweight instance to review the change, waits for the result, and shows it in the job summary, in GitHub code
scanning and, if you want, in one pull request comment. The review reads the code; it never runs it.

## Set up in five steps

1. **Make the instance reachable.** The workflow runner must be able to open your instance over HTTPS (see
   [Limits](#limits)).
2. **Store a GitHub token in Counterweight** (System settings) if the repository is private. A public repository
   needs none.
3. **Create an API token** in the console with the scopes `reviews:write` and `reviews:read`, and copy it when it is
   shown: it is shown once. Tokens start with `cwt_`. (Endpoint: `POST /api/api-tokens`.)
4. **Add two repository secrets or variables**: the token as the secret `COUNTERWEIGHT_TOKEN`, and the instance
   address (for example `https://counterweight.example.com`) as the variable `COUNTERWEIGHT_URL`.
5. **Add the workflow** below, using this repository's action (`actions/review`) from the place you keep it.

```yaml
name: Counterweight review
on:
  pull_request:
    types: [opened, synchronize, reopened]

permissions:
  contents: read
  security-events: write   # upload results to code scanning
  pull-requests: write     # only needed for comment: true

jobs:
  review:
    runs-on: ubuntu-latest   # or a self-hosted runner that can reach the instance
    steps:
      - uses: philocyber/counterweight/actions/review@<commit-sha>
        with:
          instance-url: ${{ vars.COUNTERWEIGHT_URL }}
          api-token: ${{ secrets.COUNTERWEIGHT_TOKEN }}
          depth: balanced        # light | balanced | comprehensive
          fail-on: high          # none (default) | low | medium | high | critical
          upload-sarif: true
          comment: true
          timeout: 1800          # seconds
```

The workflow does not check out the code: the instance fetches the pull request itself.

### Inputs

| Input | Default | Meaning |
|---|---|---|
| `instance-url` | required | Address of the instance. Must be `https://` unless `allow-insecure` is `true`. |
| `api-token` | required | The `cwt_` token. |
| `depth` | `balanced` | How much of the repository the reviewers read. |
| `fail-on` | `none` | The job fails when a finding at or above this severity lies inside the changed lines. |
| `upload-sarif` | `true` | Upload the results of the change to code scanning. |
| `comment` | `false` | Create or update a single comment (marked, updated in place) on the pull request. |
| `timeout` | `1800` | Seconds to wait for the review. On timeout the job fails; the review keeps running on the instance. |
| `allow-insecure` | `false` | Accept `http://`. Only for a trusted network: the token travels unencrypted. |

Exit status: `0` done, `1` a finding at or above `fail-on` is inside the change, `2` the review could not be run
(unreachable instance, rejected token, timeout). The action fails closed: an HTTP error is an error, never a pass. A
failure to write the comment is only a warning, because the results are already in the job summary.

## What the review does

- It looks at the change between the merge base and the head of the pull request, as GitHub shows it, and treats it
  as the reviewers' starting focus. They may read any other file for context and report problems the change
  introduces or exposes.
- Each finding records whether its location is inside the changed lines. The server decides this from the changed
  line ranges; the model does not. Findings outside the change are counted separately and left out of the code
  scanning upload, the comment table's main list and the `fail-on` decision. A finding whose location cannot be
  read counts as inside the change, so missing information never passes the gate.
- A second run for the same change (same repository, head commit, pull request and base) returns the existing
  review instead of starting another. The same head against another base, or in another pull request, gets its
  own review. A review that failed does not block a new one.
- The pull request text and the code are data to review, never instructions to the reviewers.
- Supplementary scanners (dependency advisories, committed secrets) run as for any code review, on the whole
  working tree.

## Limits

- **The runner must reach the instance.** Use a self-hosted runner, or an HTTPS address that GitHub-hosted runners
  can open. An instance behind NAT with GitHub-hosted runners does not work in this version. The server never
  contacts your workflow, and a watcher that polls repositories is not available yet.
- **Secrets are not available to workflows triggered from forks.** Such a run has no token and the action stops with
  a clear message. Do not use `pull_request_target` to work around that: it would run with the repository's
  permissions while handling code the contributor controls.
- **Every push costs model tokens.** A review starts on each new head commit. Set a default token or cost budget on
  the instance (tasks created this way take it; without one there is no limit), and consider limiting the workflow to labelled
  pull requests or to certain paths.
- **Results are leads for a human to confirm.** Findings are not proof, and a run that reports nothing does not show
  that the change has no problems.
- Starting a review waits while the instance clones the repository, which can take minutes for a large one.
- The change list is capped (300 files, 48 KB of diff text for the reviewers). When it is cut, the summary says so,
  and findings in files that are not listed count as inside the change.
- Only GitHub.com repositories are supported. The server posts nothing to GitHub: the workflow does, with its own
  `GITHUB_TOKEN`.

## Automation API

Used by the action; also usable from any script. Send `Authorization: Bearer cwt_...`. A token reaches only these
endpoints; it cannot open a console session or reach anything else. A console session may call them too.

| Request | Scope | Result |
|---|---|---|
| `POST /api/reviews` | `reviews:write` | Starts a review, or returns the existing one for the same change. |
| `GET /api/reviews/{id}` | `reviews:read` | Status and counts by severity, inside and outside the change. |
| `GET /api/reviews/{id}/sarif?only_change=true` | `reviews:read` | SARIF 2.1.0, paths relative to the repository root. `only_change` defaults to `false`. |
| `GET /api/reviews/{id}/summary` | `reviews:read` | Markdown for a job summary or comment. |

Request to start (give `pull_number`, or `base` and `head`; `head` with a pull number may carry the commit you expect
so that a repeated request is answered without cloning):

```json
{"repository": "acme/example", "pull_number": 7, "base": "main", "head": "<40-hex commit>", "depth": "balanced", "instructions": ""}
```

Reply (`201`, or `200` with `"existing": true`):

```json
{"id": "12", "task_id": "12", "status": "queued", "url": "https://counterweight.example.com/function/tasks/detail/?id=12"}
```

`status` is `queued`, `running`, `waiting` (with `waiting_reason`: `budget`, `approval`, `paused` or `model`),
`finished` or `failed`. Treat `id` as opaque. Set the public address under notification settings so the links point
at the address your team uses.

Tokens are managed with `GET /api/api-tokens`, `POST /api/api-tokens` (`name`, `scopes`, optional `expires_at`) and
`DELETE /api/api-tokens/{id}`, with a console session. Only a SHA-256 hash of a token is stored. Revoke a token that
leaked; `last_used_at` shows whether it was used.
