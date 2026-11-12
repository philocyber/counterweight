// Documentation content (English only). Statements here are grounded in the console components, the backend
// handlers and the public docs in the repository (README.md and docs/). Model guidance states selection
// criteria and what each template fills in; it does not rank models.
import {
  BookOpen,
  Brain,
  Cloud,
  Code,
  Coins,
  Compass,
  Cpu,
  FileText,
  GitPullRequest,
  Layers,
  LifeBuoy,
  type LucideIcon,
  Rocket,
  ShieldAlert,
  ShieldCheck,
  Target,
  Wrench,
} from "lucide-react";

// Generated from the README RunPod section: node web/scripts/sync-runpod-docs.mjs
import runpodGuide from "./_runpod.json";

export type DocBlock =
  | { type: "p"; text: string }
  | { type: "markdown"; text: string }
  | { type: "list"; items: string[] }
  | { type: "steps"; items: { title: string; body: string; href?: string }[] }
  | { type: "table"; head: string[]; rows: string[][] }
  | { type: "callout"; tone: "info" | "warn"; title: string; text: string }
  | { type: "features"; items: { title: string; href: string; body: string }[] }
  | { type: "faq"; items: { q: string; a: string }[] }
  | { type: "code"; text: string }
  // A diagram with one file per colour scheme, served from web/public.
  | { type: "figure"; light: string; dark: string; alt: string; caption?: string };

export interface DocSection {
  id: string;
  title: string;
  icon: LucideIcon;
  summary: string;
  blocks: DocBlock[];
}

export const DOC_SECTIONS: DocSection[] = [
  {
    id: "overview",
    title: "Overview",
    icon: BookOpen,
    summary: "What Counterweight is for, the two kinds of work, and who does what.",
    blocks: [
      {
        type: "p",
        text: "Counterweight is a self-hosted console where AI agents assess your own running applications and your own source code. Risky steps, such as a command that no rule covers, can wait for a person to approve them before they run. Findings leave Counterweight as downloadable reports and as Jira tickets.",
      },
      {
        type: "p",
        text: "There are two kinds of work. A black-box assessment tests a system that is running (a web application or API, or hosts and services) and needs a defined scope. A code review clones a GitHub repository and reads its source without running anything. You choose the kind when you create a task; the Assessment types section lists what changes.",
      },
      {
        type: "figure",
        light: "/diagrams/architecture-light.svg",
        dark: "/diagrams/architecture-dark.svg",
        alt: "Architecture: an operator, a CI workflow or a coding agent reaches the Counterweight server. Its agent engine tests running applications through the approval gate, execution isolation and an optional recording proxy, and reviews source code with read-only agents, scanner leads, a verifier and a fix step. Findings become reports and Jira tickets. Agents call your model provider; data stays in PostgreSQL, the data directory and a separate key directory.",
        caption:
          "How the parts fit together. Everything inside the frame runs on your server. The arrows show what it connects to: your model provider, the targets you authorize, GitHub and Jira.",
      },
      {
        type: "table",
        head: ["Role", "What it does"],
        rows: [
          [
            "Planner",
            "Reads the exploration graph and decides what to explore next. It is the only role that creates intents (directions to explore), and most wake-ups create none, which means there is nothing new to try.",
          ],
          [
            "Worker",
            "Claims one intent, does the work with the tools it has, writes facts and findings back, then stops. The number of workers per task is a system setting (default 3).",
          ],
          [
            "Code review planner and worker",
            "The same two jobs for a code review, with read-only file tools. After the review, separate verifier, fixer and patch reviewer agents check findings and write fixes.",
          ],
          [
            "Main agent",
            "The agent behind Chat. Use it to create tasks, start code reviews, ask about progress and steer work.",
          ],
          ["Goals", "Breaks a goal into parts and extracts scope from it when a task is created."],
        ],
      },
      {
        type: "p",
        text: "Agents share two records. The asset graph holds the systems found so far and is shared by all tasks. Each task also has an exploration graph of the directions tried, the facts learned and the findings. The Exploration paths tab of a task shows its exploration graph.",
      },
      {
        type: "callout",
        tone: "warn",
        title: "Authorization",
        text: "Use Counterweight only on systems and source code that you own, or that the owner has authorized you in writing to test. Testing without permission is illegal in most places. The in-app Terms of Use and the Responsible use section of the README apply.",
      },
    ],
  },
  {
    id: "quick-start",
    title: "Quick start",
    icon: Rocket,
    summary: "From a fresh install to your first reviewed finding.",
    blocks: [
      {
        type: "steps",
        items: [
          {
            title: "Install and open the console",
            body: "The recommended install is Docker Compose: run ./install.sh and choose 1) All-in-Docker install. The installer prints the console address (https://localhost:8787) and a one-time setup token. A release binary or a source build serves http://127.0.0.1:8787 instead. The README has the details for each route.",
          },
          {
            title: "Claim the installation",
            body: "The first visit opens the setup page. Paste the setup token, choose a password of at least 8 characters, accept the Terms of Use and sign in as COUNTERWEIGHT. There is one administrator account.",
          },
          {
            title: "Follow the Get started checklist",
            body: "The Dashboard shows a Get started card. Connect a model and Run your first assessment are required; Define what is in scope, Review command approvals and Connect GitHub and Jira are recommended or optional. The card disappears once a model is connected and a task exists, and Hide removes it in this browser.",
            href: "/dashboard",
          },
          {
            title: "Connect a model",
            body: "Open Models and choose Connect a model. It has three steps: where the model runs, the connection details, then test and save. The four ways to connect are a hosted API (Anthropic, OpenAI, Z.ai), a gateway (Vercel AI Gateway, Amazon Bedrock), any OpenAI-compatible endpoint (local or self-hosted servers) and ChatGPT / Codex sign-in. If no model is active yet, the one you save becomes active.",
            href: "/system/llm",
          },
          {
            title: "Define scope (black-box work)",
            body: "Open Targets and scope, add a company and list the exact hosts, IPs or CIDR ranges you are authorized to test. Black-box tasks stay inside this scope. A code review does not need it.",
            href: "/function/assets",
          },
          {
            title: "Review approval rules",
            body: "Open Approval rules. The built-in deny rules for destructive commands ship enabled. Check which tools Command interception covers and how commands that no rule matches are handled; see Safety and isolation for the exact behavior.",
            href: "/system/intercept",
          },
          {
            title: "Create the first task",
            body: "Choose New task, then choose what to assess: Web application or API, Network and hosts, Source code repository, or Something else or mixed. For a repository, enter owner/name or a GitHub URL. You can also ask in Chat, for example /code-review owner/repo.",
            href: "/function/tasks",
          },
          {
            title: "Watch it work",
            body: "Open the task. The Session tab shows the agents working. If the task pauses, a banner at the top says why and what to do; see Operating manual. The Dashboard summarizes work across tasks.",
            href: "/function/tasks",
          },
          {
            title: "Triage findings",
            body: "Open Findings. Read the evidence, then verify or retest, and set the workflow status. AI output can be wrong, so check a finding before you act on it.",
            href: "/function/findings",
          },
          {
            title: "Export",
            body: "In the task, open the Report tab to download the report as Markdown or PDF, or the findings as SARIF. Export to Jira in the task header files the task and its findings as tickets. See Reports and hand-off.",
            href: "/function/tasks",
          },
        ],
      },
      {
        type: "p",
        text: "To print the setup token again, run one of these on the machine that runs the backend. In Git Bash on Windows, prefix the first with MSYS_NO_PATHCONV=1.",
      },
      {
        type: "code",
        text: "docker compose exec counterweight /app/counterweight -setup-token   # Docker Compose\n./counterweight -setup-token                                        # release binary",
      },
    ],
  },
  {
    id: "assessment-types",
    title: "Assessment types",
    icon: Layers,
    summary: "What to choose when you create a task, and what changes with the choice.",
    blocks: [
      {
        type: "p",
        text: "New task starts with the question What do you want to assess? The answer is the assessment type. It sets the form, which agents run, which tools and skills they get, which tabs the task shows and which report layout you get. The type appears as a badge in the task header, and you can change it in the Overview tab until the task starts.",
      },
      {
        type: "table",
        head: ["Type", "Choose it for"],
        rows: [
          ["Web application or API", "A running site or API you are authorized to test."],
          ["Network and hosts", "Servers, services and ports you are authorized to test."],
          [
            "Source code repository",
            "A GitHub repository that is cloned and reviewed. Nothing from the repository is executed.",
          ],
          ["Something else or mixed", "Anything that does not fit the other choices."],
        ],
      },
      {
        type: "markdown",
        text: "### What changes per type",
      },
      {
        type: "table",
        head: [
          "What changes",
          "Web application or API",
          "Network and hosts",
          "Something else or mixed",
          "Source code repository",
        ],
        rows: [
          [
            "Form",
            "What to test, Objective, attachments, optional linked tasks, company scopes and task asset rules.",
            "Same as web application or API.",
            "Same as web application or API.",
            "Repository, Branch, tag or commit, Review depth, Additional instructions. There is no free-text target, objective, linked task or scope field.",
          ],
          [
            "Agents",
            "Planner and Worker, with one added sentence about routes, inputs, authentication and authorization.",
            "Planner and Worker, with one added sentence about hosts, services and non-HTTP protocols.",
            "Planner and Worker, with no added sentence.",
            "Code review planner and Code review worker. Verifier, fixer and patch reviewer agents run when you use verification and fixes.",
          ],
          [
            "Tools the agents can call",
            "The configured set: a shell, file tools, web fetch, the exploration and finding tools, plus any MCP servers and custom tools you enabled on the Agents page.",
            "Same as web application or API.",
            "Same as web application or API.",
            "Read-only: Read, LS, Glob and Grep, the exploration and finding tools, and skills. No shell, no web fetch or search, no MCP, no custom tools, no traffic or asset tools. The fixer can write only inside its own fix worktree.",
          ],
          [
            "Skills",
            "All skills the agents can see, except the three that only read source code.",
            "Same as web application or API.",
            "Same as web application or API.",
            "Four bundled skills only: code review methodology, DOM vulnerability static analysis, custom sanitizer audit and vulnerability assessment methodology.",
          ],
          [
            "Scope and coverage",
            "Scope you approve in Overview. Blocked targets and approval rules apply. Asset coverage is tracked by default.",
            "Same as web application or API.",
            "Same as web application or API.",
            "No network scope and no asset coverage, and the agents get no asset or scope tools. Coverage by file is not tracked: a file that no finding cites is not shown as reviewed.",
          ],
          [
            "Tabs shown",
            "All groups. Scanner leads is hidden. Mitigation is shown but only works for code reviews.",
            "Same as web application or API.",
            "Same as web application or API.",
            "Run, Findings (with Mitigation and Scanner leads), Evidence and Report. No Scope or Approvals group.",
          ],
          [
            "Report",
            "Penetration testing report, with scope and asset coverage.",
            "Same as web application or API.",
            "Same as web application or API.",
            "Code review report, with the review target (repository, branch, commit, depth) and the finding lifecycle.",
          ],
        ],
      },
      {
        type: "callout",
        tone: "info",
        title: "A code review never runs the code",
        text: "The agents of a code review read files. They do not run, build or test anything from the repository, and they have no shell and no network tools. Their file access is confined to the task's folders and the repository clone while Execution isolation is on. The Chat agent is a separate agent and is not limited this way.",
      },
    ],
  },
  {
    id: "code-review",
    title: "Code review",
    icon: Code,
    summary: "Review the source of a GitHub repository, verify findings and propose fixes.",
    blocks: [
      {
        type: "p",
        text: "A code review clones a GitHub repository, records the exact commit and lets agents read the code, trace input to its dangerous uses and report findings with file and line evidence. Only github.com repositories are accepted.",
      },
      {
        type: "markdown",
        text: "### Create a review",
      },
      {
        type: "list",
        items: [
          "In New task, choose Source code repository. Enter the Repository (owner/name or a GitHub URL), optionally a Branch, tag or commit (empty reviews the default branch), the Review depth and optional Additional instructions, then choose Clone and create task. Cloning can take a few minutes, so keep the panel open.",
          "In Chat, send /code-review owner/repo balanced. The depth is light, balanced or comprehensive and defaults to balanced.",
          "In Chat, you can also ask in plain language, for example: review the source of owner/repo. The chat agent starts the same kind of task.",
        ],
      },
      {
        type: "table",
        head: ["Depth", "What the agents read"],
        rows: [
          ["Light", "Only the security-critical files. Fastest."],
          [
            "Balanced",
            "Default. A focused review of the code that handles input, access and secrets, plus their immediate callers and callees.",
          ],
          [
            "Comprehensive",
            "The whole repository, after mapping how the application works and where its trust boundaries are. Slowest.",
          ],
        ],
      },
      {
        type: "markdown",
        text: "### Private repositories",
      },
      {
        type: "p",
        text: "A public repository needs nothing else. For a private repository, add a fine-grained GitHub token with the Contents: read permission on that repository in System settings, in the Integrations section, on the GitHub card. Counterweight tries an anonymous clone first and retries with the token only if that fails. The token reaches git through its environment, never through the address or the command line, and it is never sent to a model. Do not put credentials in the repository address; it is refused.",
      },
      {
        type: "markdown",
        text: "### Verification",
      },
      {
        type: "p",
        text: "A separate verifier agent, which did not write the finding, tries to refute it from the source. It ends with one of three results: confirmed, rejected as a false positive, or needs validation (the status stays as it was). Use Verify against the source on a finding, or switch on Automatic verification in the Retest tab. The switch is off by default. Verification reads the source; it does not run the application or its tests.",
      },
      {
        type: "markdown",
        text: "### Fixes and pull requests",
      },
      {
        type: "steps",
        items: [
          {
            title: "Generate a fix",
            body: "In the Mitigation tab, choose Generate fix on a finding. A separate fixer agent writes the change in its own copy of the code, and an automatic patch review checks it. If the finding has not been verified, you are asked whether to verify first or fix anyway. Automatic mitigation writes a fix for every finding that verification confirms; it is off by default.",
          },
          {
            title: "Open the pull request",
            body: "Nothing is published until you choose Open pull request. It needs a fine-grained GitHub token with Contents and Pull requests read and write on the reviewed repository, saved on the same GitHub card in System settings. A confirmation tells you that the pull request is opened from your GitHub account and that maintainers will see it. Check pull request refreshes its state.",
          },
          {
            title: "Re-check after the merge",
            body: "After the pull request is merged, choose Re-check merged code. An agent reads the merged source and marks the finding fixed if the vulnerability is gone. Otherwise the result is Still present or Inconclusive.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### Scanner leads",
      },
      {
        type: "p",
        text: "Two scanners run next to each code review. OSV-Scanner looks for known security advisories in the dependencies a project declares. Gitleaks looks for secrets committed in the source. They are a supplement to the agents' own review, never its basis.",
      },
      {
        type: "list",
        items: [
          "The scanners start in the background when the review is created, but the agents are told nothing until their own review has ended. If there are leads then, the task adds hints and one extra goal to verify them and keeps running. If the scanners found nothing, the agents are told nothing and the task ends as usual. A missing result does not mean the code is clean.",
          "A lead is not a finding and has no severity. It becomes a finding only when an agent verifies it in the code. The Scanner leads tab shows each lead as Not sent for review, Sent for review, Confirmed or Dismissed.",
          "Secret values are never stored or shown: only the file, line and rule. Secrets are searched in the working tree only; git history is not scanned.",
          "Dependency advisories are grouped by package. They are scanner output that has not been verified for reachability. The report lists them in their own Dependency advisories section, and possible secrets in Possible committed secrets. These sections appear in the Markdown report and the technical PDF, are not counted in the findings totals, and are not in SARIF or Jira exports.",
          "Per task, the agents receive at most 20 leads per round, 60 secret hints in total and the advisories of 8 packages. Leads beyond that stay Not sent for review, but they are still listed in the tab and in the report.",
        ],
      },
      {
        type: "p",
        text: "The Docker image includes both scanners. For a native install, download the OSV-Scanner and Gitleaks release binaries and put them on PATH, or set their absolute paths on the Code review scanners card in System settings, Integrations. The same card holds the Run scanners on code reviews switch (on by default, applies to reviews created afterwards) and shows which scanners are available and the step that fixes an unavailable one. If your Docker image is older than the scanners, rebuild it.",
      },
    ],
  },
  {
    id: "pr-reviews",
    title: "Pull request reviews",
    icon: GitPullRequest,
    summary: "Review the change of a pull request, from the console or from a GitHub Actions workflow.",
    blocks: [
      {
        type: "p",
        text: "A code review can be limited to a change: one pull request, or what differs between a base and a head. The reviewers start from the changed files and lines, and may read any other file for context. They report problems the change introduces or exposes. The code is read, never run.",
      },
      {
        type: "markdown",
        text: "### Start one from the console",
      },
      {
        type: "list",
        items: [
          "In New task, choose Source code repository and, under Review a change, pick A pull request and enter its number, or Between two points and enter a base and a head (each a branch, a tag or a commit). Whole repository remains the default.",
          "In Chat, send /code-review owner/repo#123 for a pull request, paste the pull request address, or send /code-review owner/repo main..feature for two points. A number after # now means a pull request, not a branch with that name.",
          "The task Overview shows the change: the pull request link, the base and head commits and the number of changed files.",
        ],
      },
      {
        type: "markdown",
        text: "### Inside and outside the change",
      },
      {
        type: "p",
        text: "Every finding of a change review is marked as inside or outside the changed lines. The server decides this from the stored line ranges; the model does not. In the Findings tab, a finding in code the change did not modify carries the badge Outside the change. Findings outside the change are counted separately, and they are left out of the code scanning upload and of the pass or fail decision of the workflow.",
      },
      {
        type: "markdown",
        text: "### Review every pull request from GitHub Actions",
      },
      {
        type: "steps",
        items: [
          {
            title: "Make the instance reachable",
            body: "The workflow runner must be able to open your instance over HTTPS. Use a self-hosted runner on your network, or an HTTPS address that GitHub-hosted runners can reach.",
          },
          {
            title: "Add a GitHub token for private repositories",
            body: "In System settings, Integrations, on the GitHub card. A public repository needs none.",
            href: "/system/settings",
          },
          {
            title: "Create an API token",
            body: "In System settings, Integrations, on the API tokens card, choose Create token and tick Start reviews and Read review status and results. Copy the token when it is shown: it is shown once. Tokens start with cwt_.",
            href: "/system/settings",
          },
          {
            title: "Store it in the repository",
            body: "Save the token as the repository secret COUNTERWEIGHT_TOKEN and the address of the instance as the repository variable COUNTERWEIGHT_URL.",
          },
          {
            title: "Add the workflow",
            body: "Commit the file below as .github/workflows/counterweight.yml. The workflow does not check out the code: the instance fetches the pull request itself.",
          },
        ],
      },
      {
        type: "code",
        text: "name: Counterweight review\non:\n  pull_request:\n    types: [opened, synchronize, reopened]\n\npermissions:\n  contents: read\n  security-events: write   # upload results to code scanning\n  pull-requests: write     # only needed for comment: true\n\njobs:\n  review:\n    runs-on: ubuntu-latest   # or a self-hosted runner that can reach the instance\n    steps:\n      - uses: philocyber/counterweight/actions/review@<commit-sha>\n        with:\n          instance-url: ${{ vars.COUNTERWEIGHT_URL }}\n          api-token: ${{ secrets.COUNTERWEIGHT_TOKEN }}\n          depth: balanced\n          fail-on: high\n          comment: true",
      },
      {
        type: "table",
        head: ["Input", "Default", "Meaning"],
        rows: [
          ["instance-url", "required", "Address of the instance. Must be https unless allow-insecure is true."],
          ["api-token", "required", "The cwt_ token."],
          ["depth", "balanced", "light, balanced or comprehensive."],
          [
            "fail-on",
            "none",
            "The job fails when a finding at or above this severity lies inside the changed lines: low, medium, high or critical.",
          ],
          ["upload-sarif", "true", "Upload the findings of the change to GitHub code scanning."],
          ["comment", "false", "Create or update one comment on the pull request."],
          [
            "timeout",
            "1800",
            "Seconds to wait. On timeout the job fails and the review keeps running on the instance.",
          ],
        ],
      },
      {
        type: "list",
        items: [
          "The results appear in the job summary, in code scanning and, if you ask for it, in one pull request comment that is updated in place. The comment is written by the workflow with its own GitHub token; the Counterweight server posts nothing to GitHub.",
          "A second run for the same change (same commit, pull request and base) returns the existing review, so re-running a job does not pay for the review twice. A review that failed does not block a new one.",
          "The action fails closed: an unreachable instance, a rejected token or a timeout is an error, never a pass.",
        ],
      },
      {
        type: "markdown",
        text: "### API tokens",
      },
      {
        type: "p",
        text: "An API token is for automation only. It works on the four review endpoints (start a review, read its status, its summary and its SARIF results) and nowhere else: it cannot open the console, read settings or reach any other data. Only a hash of the token is stored. The API tokens card shows when each token was last used; revoke a token that leaked or is no longer needed.",
      },
      {
        type: "callout",
        tone: "warn",
        title: "Limits",
        text: "An instance behind NAT cannot be reached by GitHub-hosted runners: use a self-hosted runner. Secrets are not available to workflows triggered from forks, and pull_request_target must not be used to work around that. Every push starts a review and costs model tokens, so set a default task budget. Only github.com repositories are supported. Findings are leads for a person to confirm, and a review that reports nothing does not show that the change is safe.",
      },
    ],
  },
  {
    id: "authorization-checks",
    title: "Authorization checks",
    icon: ShieldCheck,
    summary: "Check from a CI job, without a model, that each kind of user still gets only what it should.",
    blocks: [
      {
        type: "p",
        text: "The command counterweight authz replays a policy file against a staging environment. The file lists requests and, for each one, what every kind of user (a persona) must get back. The command sends each request as each persona and compares the answers with the policy. No model takes part, so the same policy and the same target always give the same result, and a route that opens to the wrong user fails the job. It runs in two places with the same policy format: inside a task, from the Authorization tab, and from the command line for a CI job, where it needs no running server.",
      },
      {
        type: "markdown",
        text: "### In a task",
      },
      {
        type: "steps",
        items: [
          {
            title: "Add test accounts",
            body: "Enter them in the New task form, or in the task: press Test accounts in its header, or open Findings and then Authorization. Each account has a role name (for example Admin tenant A), a username, a password and, for an API without a login request, a fixed token. The role name gives the account its key (admin_tenant_a), which stays fixed; the role name can be changed later.",
          },
          {
            title: "Write the policy",
            body: "In the matrix: type the address of the application, add the roles (each test account, and a visitor who is not signed in), say how a role signs in, then add each request and choose Allow or Deny for every role, with the status that says so. The same policy can be edited as text, for what the matrix does not show: there, name a persona after the account's key and take its credentials with ${account:admin_tenant_a.username}, ${account:admin_tenant_a.password} and ${account:admin_tenant_a.token}. Saving from the matrix rewrites the text without its comments. Saving checks the policy and lists every problem.",
          },
          {
            title: "Run the check and read the result",
            body: "Run check starts the run on the server. The result is a table of requests and roles, each role under its name, with the detail of every check that did not pass. The last 20 runs are kept, and the task report shows the latest one as an Authorization matrix section.",
          },
        ],
      },
      {
        type: "list",
        items: [
          "The password and the token of an account are encrypted in the database. The console can replace or remove them; it cannot read them back.",
          "They are never sent to a model. They are used by this check and, only for the accounts where you allow it, by a tool that lets the agents send requests as that role without seeing the secrets.",
          "A policy inside a task cannot read the server's environment, so it cannot send one of the server's own secrets to a target.",
          "The run obeys the blocked targets of the server and of the task. A blocked target is reported when the policy is saved, and nothing is sent.",
          "Accounts, policy and results are deleted with the task and are not written into a task archive package.",
        ],
      },
      {
        type: "markdown",
        text: "### Letting the agents test as a role",
      },
      {
        type: "p",
        text: "The check tests the requests you listed. The agents can look for what you did not list, such as another user's object reachable by changing an identifier, and they can do it as your test users without receiving the credentials. It is off for every account: edit an account and switch on Let the agents send requests as this role. The task's testing agents then get a tool, request_as_role. An agent names a role, a method and a path; the server signs in with the stored account, sends the request to the address of the policy and returns the answer.",
      },
      {
        type: "list",
        items: [
          "The agent never receives a password, a token or a session: they are not in its prompt, not in what it sends to the tool, and not in what the approval gate is shown.",
          "The answer is cleaned before the agent sees it: the secrets of every account of the task and the sessions obtained at sign-in, in plain and common encoded forms, and common credential shapes.",
          "The agent chooses the method, the path, its own headers and the body. It cannot choose the host and cannot set Authorization, Cookie or Host. Redirects are not followed.",
          "Every request passes the approval gate, the tool rules and the blocked targets, with its real address.",
          "A role is available only when every account its persona uses is switched on. Switching an account off, or editing an account or the policy, takes effect at once, also for a request that was waiting for approval.",
          "These requests are not stored as captured traffic.",
        ],
      },
      {
        type: "callout",
        tone: "warn",
        title: "Use test accounts only",
        text: "Cleaning the answer cannot be complete: a target that returns a credential in an unusual transformed form, or issues a new session in a field that does not look like one, shows it to the model. An agent acting as a role can do whatever that role can do on the target, within the approval gate and your rules. Enable only accounts that exist for testing, on staging, with the least privilege the role needs, and change their passwords when the engagement ends.",
      },
      {
        type: "markdown",
        text: "### From the command line, for CI",
      },
      {
        type: "steps",
        items: [
          {
            title: "Write a policy file",
            body: "Personas (anonymous, two users of different tenants, an administrator), and one case per request with the expected outcome for every persona. Keep it in the repository of the application.",
          },
          {
            title: "Check it",
            body: "counterweight authz validate policy.yaml reads the file, sends nothing, and lists the environment variables the run needs.",
          },
          {
            title: "Provide the credentials as environment variables",
            body: "From the secret store of your CI system. The policy file only names the variables; a credential written in the file is rejected.",
          },
          {
            title: "Run it against staging",
            body: "counterweight authz run -o authz-report.json policy.yaml prints a summary and writes a JSON report.",
          },
        ],
      },
      {
        type: "code",
        text: 'version: 1\n\ntarget:\n  base_url: ${env:AUTHZ_BASE_URL}\n\npersonas:\n  anonymous: {}\n  user_a:\n    login:\n      request:\n        method: POST\n        path: /api/login\n        json: { email: a@tenant-a.test, password: "${env:AUTHZ_USER_A_PASSWORD}" }\n      token: { json_pointer: /token, prefix: "Bearer " }\n    identity:\n      request: { method: GET, path: /api/me }\n      body_contains: [\'"email":"a@tenant-a.test"\']\n  admin:\n    headers:\n      Authorization: "Bearer ${env:AUTHZ_ADMIN_TOKEN}"\n    identity:\n      request: { method: GET, path: /api/me }\n      body_contains: [\'"role":"admin"\']\n\ncases:\n  - id: admin-users\n    request: { method: GET, path: /api/admin/users }\n    expect:\n      anonymous: { deny: 401 }\n      user_a: { deny: 403 }\n      admin: { allow: 200 }',
      },
      {
        type: "table",
        head: ["Exit status", "Meaning"],
        rows: [
          ["0", "Every check was sent and passed."],
          ["1", "The policy is violated: at least one check failed."],
          [
            "2",
            "The run proves nothing: invalid policy, a variable that is not set, a persona that could not sign in or prove its identity, an inconclusive check, or no check sent. Treat it as a broken job, never as a pass.",
          ],
        ],
      },
      {
        type: "markdown",
        text: "### What keeps a wrong setup from passing",
      },
      {
        type: "list",
        items: [
          "Every case states the outcome for every persona, and needs at least one persona that is allowed. If the owner cannot get through, the endpoint may be broken or the path mistyped, so the denials of that case are reported as inconclusive. A route nobody may reach is marked deny_all.",
          "Every persona with a credential proves its identity before the first case and again after the last one. An expired, wrong or misassigned credential stops the run before anything is tested.",
          "401, 403 and 404 are different answers; the policy states the one the application gives. A denial answered with a 2xx status, or with a status an allowed persona also gets, needs a text or a redirect target that tells it apart from access.",
          "A server error, a timeout or a rate limit is inconclusive, not a pass and not a violation.",
          "A request that changes data needs allow_write: true in its case. Denied personas are sent before allowed ones.",
        ],
      },
      {
        type: "markdown",
        text: "### Credentials",
      },
      {
        type: "p",
        text: "Credentials are read from environment variables and sent only to the address in base_url. Redirects are not followed and proxy settings are ignored, so nothing of a run passes through the recording proxy or is stored as captured traffic. The report shows paths as written in the policy and removes the credentials of the run from error messages and response excerpts.",
      },
      {
        type: "callout",
        tone: "warn",
        title: "Limits",
        text: "Only the requests in the file are tested: a pass says nothing about a route that is not listed. The command does not create or clean up test data, and a wrongly allowed write does change data, so run it against staging. Sign-in is a single HTTP request or a fixed token; flows that need a browser or a second factor are not supported. A failed check is not added to the findings. A failed check keeps up to 300 bytes of the response, which can be data that persona should not have seen: store the report as you would a finding. The full guide, with every option and the report format, is docs/authorization-checks.md in the repository.",
      },
    ],
  },
  {
    id: "models",
    title: "Choosing models",
    icon: Brain,
    summary: "What the agents need from a model, where it can run, and how to connect and check it.",
    blocks: [
      {
        type: "callout",
        tone: "info",
        title: "How to read this",
        text: "Counterweight does not ship a model, the project has not benchmarked models against each other, and this page does not rank them. It states what the agents need and what each way of connecting involves. Confirm your choice with the acceptance check at the end before you rely on it.",
      },
      {
        type: "p",
        text: "Every agent (planner, workers, code reviewer, verifier, reporter) calls the model profile you connect under Models. Vision is not required.",
      },
      {
        type: "table",
        head: ["Requirement", "Why it matters", "How to check"],
        rows: [
          [
            "Tool calling that stays reliable over many turns",
            "Agents act only through tools: reading files, running commands, recording findings. A model that answers in prose instead of calling a tool makes no progress.",
            "Run the acceptance check, then look in the Model call log for tool-call errors and empty turns.",
          ],
          [
            "A large context window",
            "128K tokens or more is the practical starting point. The instructions plus tool definitions already take roughly 29,000 tokens before any history or evidence.",
            "Set Context window (K) on the profile to the model's real window, up to 1000. If it is 0, Counterweight assumes 200K and compresses history early.",
          ],
          [
            "An explicit output limit on self-hosted servers",
            "Some servers reserve the whole window for the reply when no limit is sent, and then reject the request.",
            "Set Output limit, for example 8192, on self-hosted profiles. OpenAI reasoning models need the field max_completion_tokens instead of max_tokens.",
          ],
          [
            "Controllable reasoning",
            "Planning benefits from deeper reasoning. Too small an output limit can cut reasoning off before an answer.",
            "Set Thinking toggle and Reasoning effort on the profile where the provider supports them.",
          ],
          [
            "One of three API formats",
            "Anthropic Messages, OpenAI Chat Completions or OpenAI Responses. Anything that speaks one of them can be connected with a base URL.",
            "Pick the format in Advanced settings. For OpenAI models that need function calling, such as GPT-6.1 Sol, use the Responses format.",
          ],
          [
            "Predictable cost and rate limits",
            "Several workers run in parallel and every step is a model call.",
            "Set budgets (see Budgets), and check provider rate limits against your worker count. Profiles have rate fields to stay under them.",
          ],
        ],
      },
      {
        type: "markdown",
        text: "### Where inference runs",
      },
      {
        type: "table",
        head: ["Option", "What leaves your network", "Choose it when"],
        rows: [
          [
            "Hosted API (Anthropic, OpenAI, Z.ai)",
            "Prompts, tool output and target data go to that provider.",
            "You want the quickest start and your data policy allows the provider.",
          ],
          [
            "Gateway (Vercel AI Gateway, Amazon Bedrock)",
            "The same, through the gateway to the model provider.",
            "Billing or access already runs through a gateway, or you want one key for several models.",
          ],
          [
            "ChatGPT / Codex sign-in",
            "The same, to OpenAI under your workspace's terms.",
            "You have a subscription and no API key. It needs the Codex CLI on the backend host, which the Docker image does not include, so it suits native installs.",
          ],
          [
            "Local model on your workstation (Ollama, LM Studio, llama.cpp)",
            "Nothing.",
            "Data must not leave the machine and you accept slower, less capable runs. It depends on your hardware.",
          ],
          [
            "Self-hosted GPU server (vLLM)",
            "Nothing outside your infrastructure.",
            "A team shares one in-house inference server.",
          ],
          [
            "Rented GPU (vLLM on RunPod)",
            "Data goes to the GPU host you rent.",
            "You want open-weight models without buying hardware. See the RunPod setup section.",
          ],
        ],
      },
      {
        type: "p",
        text: "Whatever you choose, everything an agent reads is sent to the model you configure: source code, HTTP responses from your targets and command output. Pick an option your organization is allowed to send that material to.",
      },
      {
        type: "markdown",
        text: "### Connect a model",
      },
      {
        type: "steps",
        items: [
          {
            title: "Where the model runs",
            body: "On Models, choose Connect a model and pick Hosted API, Gateway, Local or self-hosted (Any OpenAI-compatible endpoint) or Sign in with ChatGPT. Choosing a hosted or gateway provider fills a template with the format, base URL and model.",
            href: "/system/llm",
          },
          {
            title: "Connection details",
            body: "Give the profile a name, paste the API key and enter or pick the model. Load models asks the provider which models your key can use; if that fails, type the model name. For a local server, enter its address ending in /v1 (buttons fill the Ollama, LM Studio and llama.cpp defaults) and any non-empty text as the key, because the field is required even when the server ignores it.",
          },
          {
            title: "Test and save",
            body: "Test connection sends one short request, so a wrong key, address or model shows up now and not in the middle of a task. Save without testing exists but may save a profile that does not work. For local servers, set the Context window (K) you configured on the server minus the output limit, and an Output limit of 8192.",
          },
          {
            title: "Make it active",
            body: "If no model was active, the saved one becomes active. Otherwise a switch asks Make this the active model? and your current active model stays active if you leave it off.",
          },
        ],
      },
      {
        type: "table",
        head: ["Local server", "Default address", "Before you connect"],
        rows: [
          [
            "Ollama",
            "http://localhost:11434/v1",
            "Use a model whose page lists tool support, and raise the context length: the default is far below what the agents need.",
          ],
          [
            "LM Studio",
            "http://localhost:1234/v1",
            "Start the local server, load a tool-capable model and set its context length in the load settings.",
          ],
          [
            "llama.cpp (llama-server)",
            "http://localhost:8080/v1",
            "Start it with the chat-template and function-calling options for your model, and an explicit context size.",
          ],
        ],
      },
      {
        type: "p",
        text: "If Counterweight runs in Docker, localhost points at the container itself. Use host.docker.internal instead, for example http://host.docker.internal:11434/v1; on Linux the model server must also listen on an address the container can reach. Expect to tune: start with one work agent, a small target and the light review depth. These servers have not been verified end to end by the project, and a model that passes Test connection can still fail the acceptance check.",
      },
      {
        type: "markdown",
        text: "### What the built-in templates fill in",
      },
      {
        type: "p",
        text: "A template only fills the new-profile form. Nothing is contacted, saved or activated until you test and save.",
      },
      {
        type: "table",
        head: ["Template", "Format", "Model ID", "Context window (K)", "Reasoning set"],
        rows: [
          ["Claude Opus 5.5", "anthropic", "claude-opus-5-5", "1000", "Effort medium"],
          ["Claude Fable 5.1", "anthropic", "claude-fable-5-1", "1000", "Effort high"],
          ["Claude Sonnet 5.5", "anthropic", "claude-sonnet-5-5", "1000", "Effort high"],
          ["Claude Haiku 5.5", "anthropic", "claude-haiku-5-5", "1000", "Effort medium"],
          ["GPT-6.1 Sol", "openai-responses", "gpt-6.1-sol", "922", "Effort medium"],
          ["GPT-6 Luna", "openai-responses", "gpt-6-luna", "922", "Effort medium"],
          ["GLM-5.3, General API", "openai", "glm-5.3", "1000", "Thinking enabled, effort max"],
          ["GLM-5.3, Coding Plan (reference)", "openai", "glm-5.3", "1000", "Thinking enabled, effort max"],
          ["Gateways (below)", "see below", "see below", "200", "Left unset"],
        ],
      },
      {
        type: "p",
        text: "The Z.ai Coding Plan template is a reference only: Z.ai limits that plan to officially supported tools and Counterweight is not listed, so obtain Z.ai authorization before using it. GPT-6 context windows are entered as 922K because the template subtracts the 128K maximum output from the 1.05M total.",
      },
      {
        type: "table",
        head: ["Gateway template", "Format and base URL", "Model IDs"],
        rows: [
          [
            "Vercel AI Gateway, any model",
            "openai, https://ai-gateway.vercel.sh/v1",
            "provider/model, for example anthropic/claude-sonnet-5. Load models lists them. For OpenAI models that need function calling, switch the format to openai-responses on the same URL.",
          ],
          [
            "Vercel AI Gateway, Claude",
            "anthropic, https://ai-gateway.vercel.sh (no path)",
            "provider/model, the same list.",
          ],
          [
            "Amazon Bedrock, Claude",
            "anthropic, https://bedrock-runtime.<region>.amazonaws.com/anthropic",
            "No slash, usually an inference profile such as us.anthropic.claude-sonnet-5. bedrock-runtime has no model list: type the ID.",
          ],
          [
            "Amazon Bedrock, open models",
            "openai, https://bedrock-mantle.<region>.api.aws/v1",
            "No slash, for example openai.gpt-oss-120b. Load models lists the models. A few models use /openai/v1 instead of /v1.",
          ],
        ],
      },
      {
        type: "p",
        text: "Vercel AI Gateway uses an AI Gateway API key from the Vercel dashboard. Amazon Bedrock uses a Bedrock API key: short-term keys last up to 12 hours, and a long-term key works only in the Region it was created in, so the Region in the base URL must match. Gateway templates leave thinking and effort unset and declare a 200K window; set them for the model you pick. The form warns about wrong paths, Region placeholders and model ID styles, and a failed test appends a hint naming the likely cause. The repository document docs/llm-providers.md lists every hint.",
      },
      {
        type: "markdown",
        text: "### Keep cost and load under control",
      },
      {
        type: "table",
        head: ["Control", "Where", "Effect"],
        rows: [
          [
            "Token and cost budget",
            "New task, Advanced settings; default in System settings, General",
            "Pauses a task that has used its budget. See Budgets.",
          ],
          [
            "Work agents per task",
            "System settings, General",
            "Fewer parallel workers means fewer simultaneous requests. Default 3; use 1 while evaluating a model.",
          ],
          [
            "Review depth",
            "New task, or /code-review owner/repo light",
            "Light reads only the security-critical files.",
          ],
          [
            "Request rate",
            "The profile's rate fields",
            "Keeps you under provider rate limits instead of failing on 429 responses.",
          ],
          [
            "A different model per agent",
            "Agents, model binding",
            "Bind a cheaper profile to routine roles and keep the strongest for review and verification.",
          ],
          ["Run budget", "Agents", "Caps how long a single agent keeps going. It is separate from a task budget."],
          ["Usage", "Dashboard, Model call log", "Shows what each profile consumed, including cached tokens."],
        ],
      },
      {
        type: "markdown",
        text: "### Acceptance check",
      },
      {
        type: "p",
        text: "A passing Test connection only proves that the endpoint answers. Before you trust a model with a real assessment, run this once.",
      },
      {
        type: "steps",
        items: [
          { title: "Activate the profile", body: "Choose Set active on its card." },
          {
            title: "Start a small review",
            body: "In Chat, send /code-review philocyber/Vulnerable-Flask-App light. It is a small, intentionally vulnerable public training app.",
            href: "/chat",
          },
          {
            title: "Watch the task",
            body: "Within a few minutes the Session tab should show tool calls (file reads and searches) and the Findings tab should start to fill.",
          },
        ],
      },
      {
        type: "table",
        head: ["What you see", "Likely cause"],
        rows: [
          [
            "The agent writes plans in prose and never calls a tool",
            "The model or server does not support tool calling, or the server's tool-call parser does not match the model.",
          ],
          [
            "HTTP 400 mentioning context length or max_tokens",
            "Context window (K) plus Output limit exceed what the server was started with. Lower one or raise the server's context.",
          ],
          [
            "The task stalls after the first long step",
            "The context window is too small for the tool definitions plus history. Use a larger window.",
          ],
          [
            "HTTP 401 or 403",
            "Wrong key, wrong key type for the gateway, or the model is not enabled for the account.",
          ],
          ["HTTP 429", "Lower the profile's request rate or the number of work agents."],
        ],
      },
      {
        type: "p",
        text: "When the check produces findings with code evidence, the model is usable. Whether its findings are good enough is something only a review of the results can tell you, with any model.",
      },
      {
        type: "callout",
        tone: "warn",
        title: "Reliability",
        text: "Rotation is off by default: only the active profile is used and failures stay failures. If one provider is a risk, enable rotation on the Models page and add a second profile with an API key. The active profile is tried first, then the others by priority. A failed profile cools down (60 seconds, then 5 minutes, then 30 minutes) and rejoins automatically. Agents and tasks bound to a specific profile do not rotate unless you enable Also fall back when a specified model fails.",
      },
    ],
  },
  {
    id: "runpod",
    title: "RunPod setup",
    icon: Cloud,
    summary:
      "One GPU, one persistent volume, one vLLM endpoint. Configure, verify and connect Counterweight step by step.",
    blocks: [{ type: "markdown", text: runpodGuide }],
  },
  {
    id: "features",
    title: "Feature reference",
    icon: Compass,
    summary: "Every page in the sidebar, under its current name.",
    blocks: [
      {
        type: "markdown",
        text: "### Work",
      },
      {
        type: "features",
        items: [
          {
            title: "Dashboard",
            href: "/dashboard",
            body: "Overview of your tasks and findings, and what needs your attention. It holds the Get started checklist until a model is connected and a task exists.",
          },
          {
            title: "Tasks",
            href: "/function/tasks",
            body: "Create assessments, filter them by type, and open one to run it. Each task opens with six groups of tabs: Run, Findings, Evidence, Scope, Approvals and Report.",
          },
          {
            title: "Findings",
            href: "/function/findings",
            body: "Findings from every task with evidence, severity and workflow status, plus retests and export as Markdown, ZIP, CSV, JSON, PDF or SARIF.",
          },
          {
            title: "Chat",
            href: "/chat",
            body: "Ask questions or give instructions in plain language. The chat agent can create tasks, start code reviews (/code-review) and answer questions about results.",
          },
          {
            title: "Targets and scope",
            href: "/function/assets",
            body: "The systems you are authorized to test, kept as companies with an asset scope. Black-box tasks stay inside this list.",
          },
          {
            title: "Import targets",
            href: "/function/sync",
            body: "Bring targets in from ScopeSentry by project or task instead of typing them.",
          },
          {
            title: "Files",
            href: "/function/workspace",
            body: "Browse, edit and upload the files that tasks work with, and download or delete them.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### Evidence",
      },
      {
        type: "features",
        items: [
          {
            title: "Traffic",
            href: "/function/traffic",
            body: "HTTP requests and responses recorded while testing. Recording happens only while Traffic capture is on in System settings (off by default). Records can be linked to findings as evidence.",
          },
          {
            title: "Command history",
            href: "/function/commands",
            body: "Every command the tools ran, with its output and result.",
          },
          {
            title: "Model call log",
            href: "/function/llm-records",
            body: "What was sent to and received from the model, for debugging and cost checks. The first place to look when a model misbehaves.",
          },
          { title: "Logs", href: "/system/logs", body: "Server log messages, for troubleshooting." },
        ],
      },
      {
        type: "markdown",
        text: "### Configuration",
      },
      {
        type: "features",
        items: [
          {
            title: "Models",
            href: "/system/llm",
            body: "Connect models, set their limits, prices and retry policy, and turn rotation on or off.",
          },
          {
            title: "Notifications",
            href: "/system/notify",
            body: "Send new findings to chat channels such as DingTalk, Feishu or WeCom. Its Base URL field is the console address that Jira tickets link back to.",
          },
          {
            title: "System settings",
            href: "/system/settings",
            body: "Execution isolation on top, then General, Integrations, Network, Data capture and Experimental.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### Safety",
      },
      {
        type: "features",
        items: [
          {
            title: "Approval rules",
            href: "/system/intercept",
            body: "Rules that decide which commands run on their own, need your approval or are refused. Also the model fallback reviewer and the interception scope.",
          },
          {
            title: "Blocked targets",
            href: "/system/intercept/assets",
            body: "Domains, IPs, URLs and subnets that no task may touch. Default partial-match rules block government (.gov, .gob, .gouv), military (.mil) and education (.edu) sites. The page lists examples to start a rule from: one host, every subdomain, a path, an address range.",
          },
          {
            title: "Approval history",
            href: "/system/intercept/approvals",
            body: "Commands that needed a person's decision: pending requests and past decisions. The sidebar badge counts pending approvals.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### Advanced",
      },
      {
        type: "p",
        text: "The Advanced group in the sidebar is collapsed until you open it, and it opens by itself when you are on one of its pages.",
      },
      {
        type: "features",
        items: [
          {
            title: "Agents",
            href: "/system/agents",
            body: "Prompts and settings of the built-in agents, including the code review agents, and your own chat agents. Bind an agent to a model profile and set its run budget and tools.",
          },
          {
            title: "MCP",
            href: "/system/mcp",
            body: "Connect external MCP tool servers and choose which agents can use them.",
          },
          {
            title: "Skills",
            href: "/system/skills",
            body: "Reusable instruction packs that agents can load. Code reviews can use only four of them.",
          },
          {
            title: "Tools",
            href: "/system/tools",
            body: "The tools agents can call, plus custom tools you define and optional one-click installs of recon tools.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### Documentation",
      },
      {
        type: "features",
        items: [{ title: "Docs", href: "/docs", body: "This page: how to use Counterweight, step by step." }],
      },
    ],
  },
  {
    id: "manual",
    title: "Operating manual",
    icon: Target,
    summary: "Day-to-day procedures: running tasks, pauses, findings and approval rules.",
    blocks: [
      {
        type: "callout",
        tone: "info",
        title: "Worked example",
        text: "docs/example-engagement.md in the repository is a copy-and-adapt template for one full engagement against your own authorized target, from scope to exported report, with a synthetic sample of the report format.",
      },
      {
        type: "markdown",
        text: "### Write a good goal",
      },
      {
        type: "list",
        items: [
          "State one outcome you can verify, for example confirming whether a specific class of weakness exists on named hosts.",
          "List targets explicitly. Anything outside the approved scope is a coverage proposal, not permission.",
          "Say what is off limits: destructive actions, third-party services, rate limits.",
          "For a code review there is no goal field. Put focus or exclusions in Additional instructions, for example: focus on authentication and file uploads, ignore the tests folder.",
        ],
      },
      {
        type: "markdown",
        text: "### The task workspace",
      },
      {
        type: "p",
        text: "A task page has six groups of tabs. Choose a group, then a tab inside it. Some tabs are hidden for code reviews, as noted.",
      },
      {
        type: "table",
        head: ["Tab", "Group", "Shows"],
        rows: [
          [
            "Session",
            "Run",
            "The conversation and agent execution, step by step. The Open chat button in the header opens Chat for this task.",
          ],
          [
            "Overview",
            "Run",
            "Objectives, scope and constraints, and the coverage proposals waiting for your approval. For a code review it shows the repository, branch, commit and depth. A line shows the execution isolation of the task.",
          ],
          ["Findings", "Findings", "Evidence, severity and review of the findings in this task."],
          [
            "Retest",
            "Findings",
            "Validation after remediation: past conclusions, and a button to start a retest. For code reviews it also holds the Automatic verification switch.",
          ],
          ["Mitigation", "Findings", "Verified fixes and pull requests. It only works for code reviews."],
          ["Scanner leads", "Findings", "Automated leads from the supplementary scanners. Code reviews only."],
          [
            "Results ledger",
            "Evidence",
            "Recorded conclusions, exploration attempts and findings together, searchable. A completed attempt is not a security verdict.",
          ],
          ["Exploration paths", "Evidence", "The exploration graph: which direction came from which facts."],
          ["Broadcast board", "Evidence", "The complete exploration record as a live feed."],
          ["Test assets", "Scope", "Assets associated with this task. Hidden for code reviews."],
          ["Asset coverage graph", "Scope", "Assets with recorded results. Hidden for code reviews."],
          [
            "Interception approvals",
            "Approvals",
            "Pending actions and decisions. The group shows a count of pending approvals. Hidden for code reviews.",
          ],
          ["Report", "Report", "The task report and its exports."],
        ],
      },
      {
        type: "markdown",
        text: "### Run a task",
      },
      {
        type: "steps",
        items: [
          {
            title: "Create it",
            body: "Tasks, then New task, then choose what to assess. Or ask the main agent in Chat.",
          },
          {
            title: "Approve scope (black-box)",
            body: "Open the Overview tab and review the entries the agents proposed and the automatic ones. A proposal is not permission until you approve it. Approved scope from directly linked source tasks also grants reads.",
          },
          {
            title: "Answer approvals",
            body: "A pending approval shows a count on the Approvals group of the task and on the Approval history item in the sidebar. Cancelling, or letting the five-minute wait pass, denies a command that was waiting for a person.",
          },
          {
            title: "Pause or resume",
            body: "The header button reads Pause while the task runs and Resume while it is paused. A task can also pause by itself, as described next.",
          },
          {
            title: "Review results",
            body: "Use Exploration paths to see which facts led to each finding. For black-box work, the Asset coverage graph shows which assets were never tested.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### When a task pauses",
      },
      {
        type: "p",
        text: "A banner at the top of the task page says why a task paused. A pause that Counterweight caused is saved with the task as a hold, and it ends only when you resume the task. There are three kinds.",
      },
      {
        type: "table",
        head: ["Pause", "What you see", "What to do"],
        rows: [
          [
            "Waiting for approval",
            "Tool permission required. The banner names the tool and the agent that asked. Workers stay held, and the active duration and task timeout stay frozen. The Resume button is disabled while requests are open.",
            "Choose Review tool permissions, then approve or deny each request in Interception approvals. Approving does not continue the task by itself: when the banner asks Continue after tool approval?, choose Continue task.",
          ],
          [
            "Budget reached",
            "Paused: budget reached, with the tokens or cost used. Nothing was lost; unfinished work returns to the queue.",
            "Choose Raise budget, save a higher limit, then choose Resume. Raising the budget does not resume the task. Resuming without raising it is refused.",
          ],
          [
            "Isolation unavailable",
            "Paused: isolation could not be established, with the reason. Counterweight never falls back to running without isolation.",
            "Fix the cause, or choose Open isolation settings and Check again, then choose Resume. Changing to a mode with less isolation removes protection, so decide that on its own merits.",
          ],
        ],
      },
      {
        type: "markdown",
        text: "### Handle a finding",
      },
      {
        type: "steps",
        items: [
          {
            title: "Read the evidence",
            body: "Open the finding. A black-box finding links the recorded traffic. A code finding cites files and lines at the reviewed commit.",
          },
          {
            title: "Verify or retest",
            body: "For a code review, choose Verify against the source. For black-box work, choose Retest, optionally add a fix version, test conditions or limits, and start it. A separate retester session runs and does not restart the task.",
          },
          {
            title: "Read the conclusion",
            body: "A retest ends as Still reproducible, Fixed or Inconclusive, and keeps its evidence. A retest that concludes Fixed sets the finding's status to fixed. Other outcomes leave the status as it was.",
          },
          {
            title: "Set the workflow status",
            body: "The statuses are pending, in progress, confirmed, resolved, fixed, false positive, ignored, duplicate and risk accepted. Keep them current so the list and the report reflect reality.",
          },
          {
            title: "Hand it on",
            body: "Export the findings or the report, send a finding to Jira, or generate a fix and a pull request for a code review. See Reports and hand-off.",
          },
        ],
      },
      {
        type: "markdown",
        text: "### Write approval rules",
      },
      {
        type: "list",
        items: [
          "A deny rule is never overridden by an allow rule. Matching allow and deny rules take priority over the fallback.",
          "With shell interception and a restrictive rule enabled, every unmatched Bash command needs a person, including scripts, native programs and empty interactive input.",
          "Commands whose executable comes from a variable or command substitution, generated shell programs, eval and source of generated content all need approval.",
          "For curl and wget, request methods that depend on runtime arguments or configuration files need approval. Use curl -q or --disable, or wget --no-config, as the first option to avoid the default-configuration approval.",
          "Keep allow rules narrow. Prefer one rule per repeated workflow over broad patterns.",
          "The full rules are in docs/approval-gate.md in the repository.",
        ],
      },
      {
        type: "callout",
        tone: "warn",
        title: "What approvals are not",
        text: "The approval gate is not an operating-system sandbox. It reads command syntax. It does not inspect every generated program or external script, and it does not limit what the network allows. Run Counterweight on a dedicated host, VM or container.",
      },
    ],
  },
  {
    id: "reporting",
    title: "Reports and hand-off",
    icon: FileText,
    summary: "The task report, findings exports including SARIF, and Jira tickets.",
    blocks: [
      {
        type: "p",
        text: "Results leave Counterweight in three ways: the task report, a findings export, and Jira tickets. All of them are generated from stored records. Review the findings before you share them, because AI output can be wrong.",
      },
      {
        type: "markdown",
        text: "### The task report",
      },
      {
        type: "p",
        text: "The Report tab (Report group) shows the report on screen. Export PDF opens a dialog; Download Markdown, Download SARIF and Copy sit next to it, and Refresh regenerates the report. A black-box task gets a Penetration testing report with scope and asset coverage. A code review gets a Code review report with a Review target section and a Finding lifecycle section instead.",
      },
      {
        type: "table",
        head: ["PDF option", "What it does"],
        rows: [
          [
            "Technical report",
            "Every finding on its own page with the code trace, affected lines, detailed report and an evidence appendix. For engineers and auditors.",
          ],
          [
            "Executive summary",
            "A few pages: severity overview, priorities, finding lifecycle and the findings table. For management.",
          ],
          [
            "Name the models that ran the task",
            "Off by default. Adds the model names to the provenance section. Exported reports leave them out otherwise.",
          ],
          [
            "Turn off redaction",
            "Off by default. Credentials and local paths are redacted unless you turn this on. Do it only for documents that stay internal.",
          ],
        ],
      },
      {
        type: "p",
        text: "The Markdown report redacts local paths, because the file is meant to be shared. Two lines near the top of the Markdown and PDF reports are easy to misread. Execution isolation says how the task's file access was confined, for example that file tools were confined to the task's roots while commands, extensions and network access ran on the host, or that host execution is assumed because no record exists. Execution says how the task ended: completed, stopped at the time limit, failed, in progress or not started. A task that completed with no findings is a recorded result, not proof that the assets are safe.",
      },
      {
        type: "p",
        text: "In a code review report, the Review target section gives the repository, the branch or tag (Default branch, not pinned, when none was given), the exact commit reviewed (Not recorded if it could not be read), the review depth and the method, which is source code review only with no traffic sent to live hosts. It then lists the files that findings cite. It does not list every file that was read: a file that no finding cites is not established as reviewed or free of issues, and results apply only to the commit shown.",
      },
      {
        type: "markdown",
        text: "### Findings exports",
      },
      {
        type: "p",
        text: "On Findings, choose Export, then the scope (current filtered results, all, or selected) and a format: one Markdown file, separate Markdown files in a ZIP, CSV, JSON, a PDF report (credentials redacted) or SARIF.",
      },
      {
        type: "markdown",
        text: "### SARIF and GitHub code scanning",
      },
      {
        type: "p",
        text: "SARIF is a file format that GitHub code scanning and other tools read. Download SARIF in the Report tab, or choose SARIF in the Findings export dialog. The file has one rule per vulnerability class and one result per finding. Findings that are fixed, resolved, risk accepted, ignored, false positive or duplicate stay in the file marked as suppressed, so nothing is dropped silently. Credentials and local checkout paths are redacted.",
      },
      {
        type: "list",
        items: [
          "Code review findings carry the file and line cited in their evidence, plus the repository and commit. These are the findings GitHub shows.",
          "Black-box findings have no source file. They are exported without a location, and GitHub code scanning ignores results without one. Other SARIF viewers show them.",
        ],
      },
      {
        type: "p",
        text: "To upload the file from a GitHub Actions job, give the job the permission security-events: write and add this step. The action uploads for the commit the workflow runs on.",
      },
      {
        type: "code",
        text: "- uses: github/codeql-action/upload-sarif@v3\n  with:\n    sarif_file: counterweight.sarif\n    category: counterweight",
      },
      {
        type: "p",
        text: "To upload for a specific commit from a terminal, pass ref and sha to the action, or call the GitHub REST endpoint POST /repos/{owner}/{repo}/code-scanning/sarifs with commit_sha, ref and the SARIF file gzipped and base64-encoded.",
      },
      {
        type: "markdown",
        text: "### Jira",
      },
      {
        type: "p",
        text: "Counterweight files one epic per task in the Jira space you choose, and one issue per finding under it. Only Jira Cloud is supported.",
      },
      {
        type: "steps",
        items: [
          {
            title: "Prepare an account",
            body: "Use a dedicated Atlassian account that can browse the target project, create issues, add comments and move issues between statuses, and create an API token for it.",
          },
          {
            title: "Connect once",
            body: "In System settings, Integrations, on the Jira card, enter the site (https://your-company.atlassian.net), the account email and the API token, then choose Connect. Counterweight checks the credentials before saving them. The token is stored in a private file on the server and is never shown again.",
            href: "/system/settings",
          },
          {
            title: "Set defaults (optional)",
            body: "Choose a default space and issue type, a Jira priority per severity, the days to fix per severity (the due date is the export date plus that many days; defaults are critical 7, high 30, medium 90, low 180), a default security level and extra labels. Then choose Save Jira settings.",
          },
          {
            title: "Set the console address (optional)",
            body: "Fill the Base URL field on the Notifications page. Without it, tickets name the finding ID instead of linking to it.",
            href: "/system/notify",
          },
          {
            title: "Export",
            body: "Choose Export to Jira in the task header, next to Open chat. Pick the Jira space, the issue type and who can see the issues, and whether to file open findings or all findings. The first export of a task asks for the space; after that, single findings file in one click.",
          },
        ],
      },
      {
        type: "list",
        items: [
          "Buttons: Export to Jira in the task header (on every tab), Jira on a finding's row in the Findings tab, Send to Jira in a finding's page header, and the finding card in the Report tab. Once a finding is filed, each place shows the issue key and its Jira status. Before Jira is connected, these buttons explain the integration and link to the settings.",
          "A task stays in the space of its epic, so one assessment never splits across spaces. Exporting again links the existing issues instead of filing duplicates.",
          "Each issue is written for the person who fixes it: severity, CWE classification, due date, the affected code linked to the reviewed commit, how the problem happens, how to fix it, and how to verify the fix.",
          "With Keep issues updated on (the default), Counterweight checks linked findings every five minutes and comments when a finding is confirmed, a fix pull request is opened, the pull request is merged, or the re-check finishes. A validated fix moves the issue to a Done status. If the re-check finds the issue still present, the issue is reopened. Changes made in Jira are not copied back.",
        ],
      },
      {
        type: "callout",
        tone: "warn",
        title: "Tickets contain proof of concept",
        text: "Jira issues include reproduction steps and proof of concept from the detailed report. Credentials and local paths are redacted. File them in a Jira space, or with an issue security level, that only the security team and the code owners can read.",
      },
    ],
  },
  {
    id: "budgets",
    title: "Budgets",
    icon: Coins,
    summary: "Limit how much model usage one task may consume.",
    blocks: [
      {
        type: "p",
        text: "A budget pauses a task when it has used the amount you allowed, so a long run cannot spend more than you intended. A task can have a token budget, an optional cost budget, or both.",
      },
      {
        type: "table",
        head: ["Setting", "Where", "Notes"],
        rows: [
          [
            "Token budget",
            "New task, Advanced settings",
            "Input plus output tokens of every model call of the task: planner, workers, chat, goal breakdown, compaction and retries. Retests, verification and fix runs of a task's findings count toward that task. Empty uses the default; 0 means no limit.",
          ],
          [
            "Default token budget",
            "System settings, General",
            "Given to new tasks that do not set their own. 0 means no limit, which is the default. It does not change existing tasks.",
          ],
          [
            "Cost budget",
            "New task, Advanced settings",
            "Optional, with a three-letter currency (USD by default). The field appears only once at least one model profile has prices. Empty or 0 means no limit.",
          ],
          [
            "Prices",
            "Models, on a profile: Prices for cost estimates",
            "Input, Cached input and Output, per million tokens, and a currency. All optional. Counterweight has no built-in price list, so enter what your provider charges. Cost is estimated only when Input and Output are both set. A price change affects later calls only.",
          ],
        ],
      },
      {
        type: "markdown",
        text: "### What happens as a task uses its budget",
      },
      {
        type: "list",
        items: [
          "While a limit is set, the task header shows a Budget line: tokens used of the limit, estimated cost, a progress bar and the state. Edit budget in that line changes the limits.",
          "At 80 percent, one Budget warning appears in the activity and the state reads the percentage used. Editing a budget arms the warning again.",
          "At the limit, the task pauses with a hold. Calls already in flight finish, then the task stops. The banner reads Paused: budget reached. Nothing is lost, and unfinished work returns to the queue as it does after a normal pause. Near the limit, calls run one at a time, so a task can go over the limit by roughly the size of one call.",
          "To continue, choose Raise budget, save a higher limit (0 removes a limit), then choose Resume. Raising does not resume the task by itself. Resuming without raising is refused with a message that says how much was used.",
          "While the budget is used up, other work on that task is refused too: rerunning work, following up on a finding, retests, manual verification and fix generation. Automatic verification and fixes are skipped.",
          "The post-merge re-check and side questions are not stopped by a budget.",
        ],
      },
      {
        type: "callout",
        tone: "info",
        title: "What cost unknown means",
        text: "A call whose model profile has no prices, or prices in another currency, has tokens but no known cost. It adds nothing to the estimated cost, and the header says Cost unknown for some calls. A cost budget cannot stop those calls; only the token budget guards them. If you rely on a cost budget, set prices on every profile a task can use.",
      },
      {
        type: "p",
        text: "A task budget is separate from the run budget on the Agents page, which caps how long a single agent keeps going.",
      },
    ],
  },
  {
    id: "safety",
    title: "Safety and isolation",
    icon: ShieldAlert,
    summary: "The controls, in order of importance, and what they do not cover.",
    blocks: [
      {
        type: "markdown",
        text: "### Execution isolation",
      },
      {
        type: "p",
        text: "Execution isolation limits what the agents of a task can reach on the server while they run. It is the first card of System settings, labelled Security control, and it has three modes.",
      },
      {
        type: "table",
        head: ["Mode", "Files", "Commands", "Choose it when"],
        rows: [
          [
            "Off",
            "The server's own access",
            "Run on the server",
            "You are debugging the tool itself. Not recommended.",
          ],
          [
            "Confined files (default)",
            "Only the task's own folders and, for a code review, the repository clone",
            "Run on the server, with the known server credentials masked",
            "Docker is not available to the server.",
          ],
          [
            "Container",
            "Only the task's own folders",
            "Run in the task's own container, without the server's environment",
            "Counterweight is installed natively and Docker Engine or Docker Desktop is running.",
          ],
        ],
      },
      {
        type: "list",
        items: [
          "Confined files: the file tools of a task's agents (read, write, edit, search) are confined to the task's own folders and, for a code review, the repository clone under review. Skills are readable but never writable.",
          "Container: each task gets its own container for its shell commands. It runs as an unprivileged user with no capabilities, on a read-only system with a private temporary folder and limits on processes, memory and CPU. It sees only the task's own folders. Every command starts from an empty environment, so the model keys and database credentials of the server are not there to read. A command that runs past its time limit is stopped by removing the container, which also stops anything it left running. The container is removed when the task finishes, is paused, deleted or archived, and when the server shuts down.",
          "A self-test checks each mode before it can be used. For confined files it plants a file outside the allowed folders and tries to read and write it through the real file tools by absolute path, by path traversal and by symbolic link. For the container it starts a real container and checks from inside that a file elsewhere on the host cannot be reached, that a server environment variable is absent, that writing outside the task folder fails, that the process has no privileges, that a timeout stops the command and its children, and that files are shared with the server both ways. It runs at startup and when you choose Check again. Each mode reads Ready, Not available or Failed, with the reason and the step that fixes it.",
          "It fails closed. If the selected mode cannot be set up for a task, the task pauses with Paused: isolation could not be established. It never falls back to running with less isolation.",
          "It is recorded. Every time a task starts or resumes, its mode is recorded and cannot be edited later. The task header shows a badge (Container, Files confined, Not isolated, Isolation not recorded, or Ran in more than one mode) with the facts behind it, and reports carry an Execution isolation line, including the image the container used.",
          "A change of mode applies from each task's next start or resume. A run already in progress keeps its mode.",
        ],
      },
      {
        type: "markdown",
        text: "### Turn on container mode",
      },
      {
        type: "steps",
        items: [
          {
            title: "Build the image once",
            body: "On a native install, run the command below from the Counterweight folder. The server never downloads an image by itself. Set COUNTERWEIGHT_SANDBOX_IMAGE to use another local image.",
          },
          {
            title: "Check again",
            body: "In System settings, on the Execution isolation card, choose Check again. Container changes to Ready and shows the image.",
            href: "/system/settings",
          },
          {
            title: "Select Container",
            body: "The server runs the self-test again before it accepts the change. If it refuses, the reason is shown and the previous mode stays.",
          },
        ],
      },
      {
        type: "code",
        text: "docker build -t counterweight:local .",
      },
      {
        type: "callout",
        tone: "warn",
        title: "What isolation does not do",
        text: "No mode restricts the network, and none is a virtual machine. Run Counterweight on a dedicated host, VM or container that holds nothing else of value, and keep the console off the public internet.",
      },
      {
        type: "list",
        items: [
          "Network: nothing here restricts where commands or HTTP tools connect, in any mode. A container can open outgoing connections, including to services on the host or the local network. Scope, blocked targets and approval rules apply separately.",
          "Container mode, tools: background tasks, interactive shells, custom command and script tools, MCP servers, and the platform-management and cross-task tools are withheld while the mode is on, because they would still start a process on the server. Web fetch is withheld too, because it would send its requests from the server's own network; commands in the container have curl. File tools, structured HTTP tools, findings and graph tools, and web search stay available. A chat you open yourself, not started from a task, keeps the platform-management and cross-task tools; its commands run in that conversation's own container.",
          "Container mode, traffic: traffic capture does not record commands run in the container yet. Requests the server makes itself are captured as before.",
          "Container mode, Docker Compose: a server that itself runs under Docker Compose cannot use the mode yet. Counterweight does not mount the Docker socket into the application, because that would hand it the host.",
          "Container mode, platforms: it has been verified with a real container on Docker Desktop for Windows. Linux and macOS have not been verified end to end.",
          "Confined files, commands: for the assessment types that have a shell (web application or API, network and hosts, something else), commands, extensions such as MCP servers and custom tools run on the host with the privileges of the Counterweight process. A shell can read files the file tools cannot.",
          "Code reviews have no shell in any mode, so they record Files confined even when Container is selected: no command ran, so no container was used.",
          "Hard links: a hard link planted inside a task folder that points to a file outside it is not blocked.",
          "Conversations that are not part of a task, such as general Chat and custom agents, are not confined.",
          "The Docker Compose install is packaging, not isolation: it runs the application and its research tools in one container with database and provider credentials in its environment.",
        ],
      },
      {
        type: "markdown",
        text: "### Scope and blocked targets",
      },
      {
        type: "list",
        items: [
          "Scope entries that agents or automatic discovery propose are proposals. They count only after you approve them in the task Overview.",
          "Hosts, IPs and CIDR ranges that you write in a task's text, and targets you select, are treated as your authorization for that task. Exact hostnames stay exact: they do not authorize the parent domain.",
          "Blocked targets removes domains, IPs, URLs and subnets from the agents' reach, for all tasks. A task can also carry its own block and allow rules.",
          "These checks apply to the native HTTP tools. They do not confine arbitrary programs that a shell runs.",
        ],
      },
      {
        type: "markdown",
        text: "### Approval rules and the approval gate",
      },
      {
        type: "list",
        items: [
          "The built-in deny rules ship enabled. They block clearly destructive operations such as recursive forced deletion, formatting disks, shutdown, flushing firewall rules, dropping databases and HTTP DELETE. They are patterns you can review, edit or disable, and patterns cannot catch every equivalent command.",
          "With shell interception and a restrictive rule enabled, every unmatched shell command waits for a person. The model reviewer cannot approve that fallback. Cancelling, or letting the five-minute wait pass, denies it. Your explicit allow and deny rules keep priority.",
          "That is not the whole picture. A tool call that no rule matches is allowed when the model reviewer is off. A reviewer that fails applies its failure policy, which defaults to asking a person. Add explicit ask or deny rules for anything that must always wait for a person.",
          "When a task tool needs permission, the task pauses with a hold that survives restarts. Approving does not resume the task; you continue it yourself.",
          "The gate reads command syntax. It is not an operating-system sandbox. docs/approval-gate.md lists the exact rules.",
        ],
      },
      {
        type: "markdown",
        text: "### Sessions and sign-in",
      },
      {
        type: "list",
        items: [
          "There is one administrator, COUNTERWEIGHT. Passwords are at least 8 characters and at most 72 bytes.",
          "Sign out in the account menu revokes your session on the server. Sign out everywhere revokes every session. Changing or resetting the password makes every other session sign in again.",
          "Live views open their event stream with a stream ticket: a random credential valid for about a minute and usable once, so no reusable credential appears in a URL or a log.",
          "Plain HTTP is accepted only on a loopback address. Any other address needs HTTPS.",
        ],
      },
      {
        type: "markdown",
        text: "### Stored credentials",
      },
      {
        type: "list",
        items: [
          "Model API keys and proxy URLs, MCP environment and header values, notification channel settings and web-search keys are write-only in the console: after you save them, only a hint is shown.",
          "They are encrypted in the database. The key is a private file kept outside the database and outside the data directory: the keys volume (/app/keys) under Docker Compose, or beside the binary for a native install. A database backup without the key does not reveal them.",
          "Back up the key directory with the database, but store it separately. If the key is lost, the server refuses to start; restore the key and start again.",
          "The GitHub token and the Jira credential are kept in private files on the server. They are never returned to the browser and never sent to a model.",
          "Values typed into free-form fields are not encrypted: an MCP server's arguments or URL, a model base URL, and recorded model requests. Do not put tokens there, and do not paste secrets into task goals or chat.",
        ],
      },
      {
        type: "markdown",
        text: "### Update signatures",
      },
      {
        type: "p",
        text: "The in-app updater downloads the release over HTTPS from GitHub only and checks it against the release's SHA256SUMS. When the build embeds the maintainer's release keys, it also requires an Ed25519 signature on that file and refuses the update otherwise. Builds without embedded keys, which is how this repository ships until a key is published, check the checksum only, and that detects corruption but not a replaced release. The update screen says which case applies. An update restarts the application and interrupts running tasks, so update when idle.",
      },
      {
        type: "callout",
        tone: "info",
        title: "AI can be wrong",
        text: "Findings and actions can be incomplete, inaccurate or harmful. Verify before you act, and use a staging environment when production impact is not acceptable.",
      },
    ],
  },
  {
    id: "system",
    title: "Configuration",
    icon: Wrench,
    summary: "System settings, deployment facts and the environment variables you are likely to need.",
    blocks: [
      {
        type: "p",
        text: "Settings live in three places. Command-line flags and environment variables are read at start. config.json holds only the database connection and the skill directory. The console holds everything else.",
      },
      {
        type: "markdown",
        text: "### The System settings page",
      },
      {
        type: "p",
        text: "The Execution isolation card is on top (see Safety and isolation). Below it are five sections.",
      },
      {
        type: "table",
        head: ["Section", "What lives in it"],
        rows: [
          [
            "General",
            "Version and updates; the number of worker agents per task (default 3, applies to tasks started afterwards); the Python interpreter for custom scripts; the default token budget for new tasks; and the Conversation send shortcut, which is stored only in this browser.",
          ],
          [
            "Integrations",
            "The GitHub token (private repositories, pull requests), the Jira connection, the Code review scanners card and the API tokens that let a CI workflow start pull request reviews.",
          ],
          [
            "Network",
            "The global proxy, which agents' target traffic and the updater use, and Web search: a master switch and its source. Search queries leave this server and go to the provider you choose.",
          ],
          [
            "Data capture",
            "Traffic capture (off by default), Agent automatic traffic binding (off by default, uses more tokens) and Operational constraint injection for the planner and worker (both on by default). Recorded traffic can contain passwords, tokens and personal data from the target.",
          ],
          [
            "Experimental",
            "noa context compression, which uses extra model calls to summarize long conversations. It is off by default and may change.",
          ],
        ],
      },
      {
        type: "markdown",
        text: "### Deployment facts",
      },
      {
        type: "table",
        head: ["Topic", "Facts"],
        rows: [
          [
            "Ports",
            "8787 serves the console and API. 8788 is the recording proxy. Docker Compose publishes both on 127.0.0.1 only. Change them with -addr and -proxy, for example ./start.sh -addr 127.0.0.1:9000 -proxy 127.0.0.1:9001.",
          ],
          [
            "Docker Compose volumes",
            "pgdata holds PostgreSQL. keys holds the session signing key and the credential encryption key. ./data holds traffic evidence, task files and code review clones. ./skills holds skills. ./tls holds the certificate, mounted read-only.",
          ],
          [
            "HTTPS",
            "Docker Compose always serves HTTPS and needs tls/server.pem and tls/server-key.pem before the first start. A native install serves plain HTTP on loopback only; any other address needs -tls-cert and -tls-key. HTTP and HTTPS origins keep separate browser storage, so you sign in again when you switch.",
          ],
          [
            "Database",
            "PostgreSQL is required. The schema is applied on every start, so a restart is the migration, and it does not roll back. Back up PostgreSQL, ./data and the keys before you update.",
          ],
          [
            "Updating",
            "Docker: git pull, then docker compose up -d --build counterweight (or ./update.sh). Release binary: System settings, General, Version and updates. Native installs start through start.sh or start.bat so that updates can restart the application.",
          ],
          [
            "Language",
            "The console is available in English and Spanish. This page, backend messages and reports are in English.",
          ],
        ],
      },
      {
        type: "code",
        text: "./start.sh -addr 127.0.0.1:8787 -proxy 127.0.0.1:8788",
      },
      {
        type: "markdown",
        text: "### Environment variables you are likely to need",
      },
      {
        type: "table",
        head: ["Variable", "Purpose"],
        rows: [
          [
            "POSTGRES_PASSWORD",
            "Docker: the database password in .env. Compose refuses to start without it. Native installs use COUNTERWEIGHT_PG_DSN or config.json instead.",
          ],
          [
            "ANTHROPIC_API_KEY, OPENAI_API_KEY",
            "Create a default model profile at first start. You can connect models in the console instead.",
          ],
          ["COUNTERWEIGHT_LLM_PROVIDER, _MODEL, _BASE_URL, _PROXY", "Optional settings for that default profile."],
          [
            "COUNTERWEIGHT_KEY_DIR",
            "Where the session signing key and the credential encryption key live. Docker Compose sets it to the keys volume.",
          ],
          [
            "COUNTERWEIGHT_SECRETS_KEY",
            "A base64 key that replaces the secrets.key file, for deployments that inject secrets.",
          ],
          [
            "COUNTERWEIGHT_RESET_STORED_CREDENTIALS",
            "Set to 1 for one start to clear stored credentials that can no longer be decrypted after the key was lost. Remove it afterwards.",
          ],
          ["COUNTERWEIGHT_PROXY_CLIENT_CIDRS", "Network ranges allowed to use the recording proxy besides loopback."],
          [
            "COUNTERWEIGHT_TRAFFIC_*, COUNTERWEIGHT_TASK_ARCHIVE_BYTES",
            "Storage limits for recorded traffic and task archives.",
          ],
          ["COUNTERWEIGHT_CODEX_BINARY", "Path to the Codex CLI for ChatGPT / Codex sign-in, if it is not on PATH."],
        ],
      },
      {
        type: "p",
        text: "The complete reference, with defaults, flags, TLS, the recording proxy and storage limits, is docs/configuration.md in the repository. Backups, password reset, updates and key rotation are in docs/operations.md.",
      },
    ],
  },
  {
    id: "troubleshooting",
    title: "Troubleshooting",
    icon: LifeBuoy,
    summary: "Common problems and where to look.",
    blocks: [
      {
        type: "faq",
        items: [
          {
            q: "A task starts but nothing happens.",
            a: "Check that a model is active (saving is not enough) and that Test connection succeeds on the Models page. Then open the Model call log to see the actual request and error. Also check the task page for a banner: the task may be paused.",
          },
          {
            q: "A task is paused and I do not know why.",
            a: "Read the banner at the top of the task page. Tool permission required means a command is waiting for you: review it in Interception approvals, then choose Continue task. Paused: budget reached means the task used its budget: raise it, then choose Resume. Paused: isolation could not be established means the selected isolation mode could not be set up for this task: read the reason, fix it or check again in System settings, then choose Resume. With no banner, the Pause button was used: choose Resume.",
          },
          {
            q: "Live views do not update.",
            a: "Live views (task activity, logs) open their event stream with a short-lived ticket. If they stay still after an upgrade, restart the backend so it runs the version you are looking at, then reload the page. If you signed out everywhere or changed the password, sign in again. If you reach Counterweight through a reverse proxy, it must pass event streams through; docs/configuration.md describes NEXT_PUBLIC_SSE_BASE.",
          },
          {
            q: "A code review of a private repository fails to clone.",
            a: "Add a fine-grained GitHub token with Contents: read on that repository in System settings, Integrations, GitHub, then try again. Counterweight clones anonymously first and retries with the token, and the error message tells you to add a token when none is stored. Also check that the address is a github.com repository over HTTPS with no credentials in it, and that the branch, tag or commit exists.",
          },
          {
            q: "Scanner leads show as unavailable or empty.",
            a: "Open the Code review scanners card in System settings, Integrations. It shows whether OSV-Scanner and Gitleaks are available and the step that fixes an unavailable one. Under Docker, rebuild the image if it predates the scanners. On a native install, download the release binaries and put them on PATH or set their paths on that card. Check that Run scanners on code reviews is on, and note that it applies to reviews created afterwards. An empty tab does not mean the code is clean.",
          },
          {
            q: "The server refuses to start and names a missing key.",
            a: "The database holds encrypted credentials but the encryption key is missing. Restore secrets.key from your backup into the key directory (the keys volume under Docker Compose) or set COUNTERWEIGHT_SECRETS_KEY, then start again. If the key cannot be recovered, start once with COUNTERWEIGHT_RESET_STORED_CREDENTIALS=1: unreadable credentials are cleared, a new key is used, and you enter the API keys, proxies and channel settings again. Tasks, findings and assets are not affected. Remove the variable afterwards.",
          },
          {
            q: "A card at the bottom right invites me to star the project on GitHub.",
            a: "It appears once, after the installation has at least two tasks and one finding. Later hides it for two weeks. The close button and Do not ask again end it for good, for every browser. Starring uses your stored GitHub token if there is one (it needs the account permission Starring: read and write); otherwise the button opens the repository page. With a token stored, Counterweight asks GitHub once whether you already starred the project and does not show the card if you have. Without a stored GitHub token it sends nothing to GitHub for this feature, and Counterweight has no telemetry.",
          },
          {
            q: "The model keeps returning empty turns or malformed tool calls.",
            a: "Try a model with stronger tool calling, lower the reasoning effort, or raise the output limit. Run the acceptance check on the profile, and compare calls in the Model call log between models before changing prompts.",
          },
          {
            q: "Requests fail with unsupported_parameter.",
            a: "OpenAI reasoning models reject max_tokens. Set the Limit field name on the profile to max_completion_tokens.",
          },
          {
            q: "Context limit exceeded messages appear.",
            a: "Counterweight reduces history and retries. If it recurs, make sure Context window (K) matches the model, or switch to a longer-context model.",
          },
          {
            q: "Commands hang with no output.",
            a: "Check Approval history for a pending request. Unmatched shell commands wait for a person and are denied after five minutes without an answer.",
          },
          {
            q: "A provider keeps failing.",
            a: "Failed profiles cool down and are skipped, then rejoin. Turn on rotation and add a second profile with an API key for resilience.",
          },
          {
            q: "I cannot find an asset in coverage.",
            a: "Scope is approved per task. Check the task Overview for pending proposals, and Blocked targets for exclusions. Code reviews have no asset coverage.",
          },
          {
            q: "GitHub code scanning shows nothing from my SARIF upload.",
            a: "GitHub ignores results without a source location. Only code review findings carry one; black-box findings are in the file but not shown. Also check that the upload job has the permission security-events: write.",
          },
          {
            q: "I forgot the administrator password.",
            a: "Run ./reset-password.sh -m docker for Docker, or ./reset-password.sh -m local --backend ./counterweight for a native install. No restart is needed; other sessions sign in again.",
          },
          {
            q: "The Docker container stops right after it starts.",
            a: "Run docker compose logs counterweight. The usual causes are a missing or invalid tls/server.pem or tls/server-key.pem, or POSTGRES_PASSWORD not set in .env.",
          },
          {
            q: "The browser shows a certificate warning.",
            a: "That is expected with the self-signed certificate the installer creates. Accept it once, or create a locally trusted certificate with mkcert as docs/configuration.md describes.",
          },
        ],
      },
    ],
  },
  {
    id: "glossary",
    title: "Glossary",
    icon: Cpu,
    summary: "Terms used across the interface.",
    blocks: [
      {
        type: "table",
        head: ["Term", "Meaning"],
        rows: [
          [
            "Assessment type",
            "What a task assesses: Web application or API, Network and hosts, Source code repository (a code review) or Something else or mixed. It sets the form, agents, tools, tabs and report layout.",
          ],
          [
            "Black-box assessment",
            "Testing a running system from the outside. It uses a scope, approval rules and, optionally, recorded traffic.",
          ],
          ["Code review", "An assessment that reads a repository's source and executes nothing from it."],
          ["Intent", "One direction for a worker to explore. Created only by the planner."],
          ["Frontier", "The queue of intents waiting to be claimed."],
          ["Fact", "An observation an agent wrote back to the exploration graph."],
          ["Finding", "A vulnerability an agent recorded with evidence, a severity and a workflow status."],
          [
            "Lead",
            "A pointer from a supplementary scanner, such as a possible committed secret. It is not a finding, has no severity and becomes a finding only when an agent verifies it in the code.",
          ],
          [
            "Advisory",
            "A known security advisory that a scanner matched to a dependency. It is not verified for reachability.",
          ],
          ["Anchor", "A link between an exploration node and an asset."],
          ["Coverage", "Which in-scope assets have been tested. Code reviews have no asset coverage."],
          [
            "Verification",
            "A check of a code review finding by a separate verifier agent that tries to refute it from the source.",
          ],
          [
            "Retest",
            "A separate session that checks again whether a finding can still be reproduced. Conclusions: Still reproducible, Fixed or Inconclusive.",
          ],
          [
            "Mitigation",
            "A fix for a code review finding, written by a fixer agent, reviewed by a patch reviewer and published as a pull request only when you choose.",
          ],
          [
            "Execution mode",
            "How a task's agents ran, recorded each time the task starts or resumes: files confined (file tools limited to the task's folders; commands and network on the host), host (nothing confined), or container (reserved, not available).",
          ],
          [
            "Budget",
            "A limit on the tokens, or the estimated cost, that one task may use. Reaching it pauses the task.",
          ],
          [
            "Hold",
            "A saved pause of a task with a reason: waiting for tool approval, budget reached, or isolation unavailable. It survives restarts and ends when you resume the task.",
          ],
          [
            "Stream ticket",
            "A random, single-use credential valid for about a minute that the console requests to open a live event stream.",
          ],
          [
            "SARIF",
            "A standard file format for static analysis results, read by GitHub code scanning and other tools.",
          ],
          ["Profile", "A saved model connection: format, base URL, model, key, reasoning, limits and optional prices."],
          ["Skill", "Reusable guidance an agent can load for a specific kind of work."],
          ["MCP", "Model Context Protocol servers that add tools for agents."],
          ["Interception", "The approval gate in front of tool execution, configured on the Approval rules page."],
          [
            "Former page names",
            "Interception rules is now Approval rules. Asset blocking is Blocked targets. Approval records is Approval history. LLM recordings is Model call log. Tool executions is Command history. Assets is Targets and scope. Asset sync is Import targets. Workspace is Files. LLM is Models.",
          ],
        ],
      },
    ],
  },
];
