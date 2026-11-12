# Jira Cloud integration

Counterweight files findings as Jira issues for the developers who own the code, and keeps those
issues updated as fixes move forward.

- **You choose the Jira space.** Large organizations split Jira into spaces (projects) per team or
  business unit. Each export chooses the space, the issue type and who can see the issues; defaults
  from Settings are preselected.
- **One epic per task.** A task (for example, the security review of one repository) becomes an
  epic in the chosen space. Each finding becomes a child issue under it. Once the epic exists, the
  task's other findings go to the same space, so one assessment never splits across spaces.
- **One click per finding, or one click per task.** **Export to Jira** in the task header (next to
  **Open Chat**, on every tab) files the epic and every open finding at once. To file a single
  finding, use **Jira** on its row in the **Findings** tab, **Send to Jira** in the finding's page
  header, or the finding card in the **Report** tab. The first export of a task asks for the space;
  after that, single findings file in one click. Once filed, each place shows the issue key and its
  Jira status. Before Jira is connected, these buttons explain the integration and link to
  **System settings → Jira**.
- **No duplicates.** Counterweight remembers which issue belongs to which finding. Exporting again
  links the existing issue instead of filing a new one; if an issue was deleted in Jira, the next
  export files a new one.
- **Kept up to date.** When a fix is proposed, merged or re-checked, Counterweight comments on the
  issue. When the re-check confirms the fix, the issue moves to a Done status; if the issue is
  still present after the merge, it is reopened.

Only Jira Cloud is supported. Counterweight uses the Jira REST API with an Atlassian API token.

## What a ticket contains

Each issue is written for the person who will fix it:

| Part | Content |
| --- | --- |
| Summary panel | Severity, CWE classification, remediation due date, status, repository and commit, link back to the finding |
| What is wrong | The finding's summary |
| Affected code | Files and line ranges, linked to the reviewed commit on GitHub |
| How it happens | The source-to-sink code trace, step by step, with root cause and conditions |
| How to fix | The remediation recorded with the evidence |
| Detailed report and reproduction | The full report, including reproduction steps and proof of concept |
| How to verify the fix | What to run in Counterweight after the fix ships |
| References | CWE and OWASP Top 10 pages, and the finding in Counterweight |

Credentials and local paths are redacted, as in PDF exports. Issues are labelled `counterweight`,
`security`, `severity-<level>` and `cwe-<id>`, plus any labels you add.

> [!IMPORTANT]
> Tickets include reproduction steps and proof of concept. Use a Jira project, or an issue security
> level, that only the security team and the code owners can read.

## Setup

1. **Create an account for Counterweight (recommended).** Use a dedicated Atlassian account with
   permission to browse the target project, create issues, add comments and transition issues.
2. **Create an API token** for that account at
   [id.atlassian.com/manage-profile/security/api-tokens](https://id.atlassian.com/manage-profile/security/api-tokens).
3. In Counterweight, open **System settings → Jira** and enter the site URL
   (`https://your-company.atlassian.net`), the account email and the API token, then click
   **Connect**. Counterweight checks the credentials before saving them.
4. Optionally choose a **default space** and **default issue type** (for example Bug, Task or a
   custom Vulnerability type). They are preselected when exporting; every export can pick another
   space. The epic type of each space is detected automatically.
5. Optionally:
   - map each severity to a **Jira priority**;
   - set the **days to fix** per severity; the due date is the export date plus that many days
     (default: critical 7, high 30, medium 90, low 180);
   - choose a default **security level** for the default space;
   - add **extra labels**, such as the owning team.
6. Click **Save Jira settings**.

To link issues back to Counterweight, set the console's public address in
the **Base URL** field of the **Notifications** page. Without it, tickets name the finding ID instead of linking it.

The API token is stored in a private file on the Counterweight server, like the GitHub token. It is
never returned to the browser, sent to a model or written to logs, and it is only sent to the
configured site over HTTPS. Changing the site requires entering its token again.

## Spaces, issue types and visibility

- **Space and issue type** are chosen in the export dialog. A task stays in the space of its epic.
- **Who can see the issues**: if the space uses issue security levels, the dialog lists them. The
  choice is remembered for the task. If the chosen level cannot be applied in the space, the export
  fails instead of filing issues with wider visibility.
- **Priorities** mapped in Settings are applied in every space that offers them; elsewhere the
  space's default priority is used.

## Keeping issues updated

With **Keep issues updated** on (the default), Counterweight checks linked findings every five
minutes and posts one comment per change:

| Change in Counterweight | Comment | Jira status |
| --- | --- | --- |
| Verification confirms the finding | Confirmed | Unchanged |
| A fix pull request is opened | Link to the pull request | Unchanged |
| The pull request is merged | Re-check pending | Unchanged |
| Re-check finds the path closed | Fix validated | Moved to a Done status |
| Re-check finds the issue still present | Fix needs more work | Moved back to In Progress or To Do |
| Finding marked fixed or resolved | Status change | Moved to a Done status |
| Verification rejects the finding | Possible false positive | Unchanged, for a person to decide |

Transitions use Jira's status categories (To Do, In Progress, Done), so they work with custom
workflows. When the workflow offers no matching transition, only the comment is posted. Changes made
in Jira are not copied back into Counterweight.

## Troubleshooting

- **"The email or API token was rejected"**: the token was revoked or belongs to another account.
- **"The Jira issue type requires fields Counterweight does not fill"**: the issue type has required
  custom fields. Give them a default value in Jira, or choose another issue type.
- **"This task is filed in the Jira space …"**: the task's epic already lives in another space; its
  findings follow the epic.
- **"The chosen security level is not available"**: pick a level that exists in that space, or the
  space default.
- **Issues are filed without an epic**: the space has no epic-level issue type, or the chosen
  issue type cannot have an epic parent.
- **No priority or due date on the issue**: the field is not on the issue type's create screen.
  Add it to the screen in Jira.
