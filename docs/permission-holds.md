# Task permission holds

When a task tool requests human permission, Counterweight persists a permission hold and cancels the task's current workers, planner, main-agent turn, and background compaction. The workers retain their transcripts and return interrupted intents to the frontier. Pending permission requests survive cancellation and server restarts.

The task page displays **Tool permission required**, names the tool and requesting agent, and links to **Review tool permissions**. Review the tool arguments there and approve or deny each pending request. **Resume** stays disabled until all requests are resolved. An approval, denial, or configured approval timeout leaves the task paused until the user clicks Resume. The task page then asks whether to resume the scan, because approving the tool does not continue it.

Task and session active duration exclude recorded permission holds. The task timeout budget also freezes until Resume, including across restarts. Simultaneous requests share one hold, so the wait is counted once. Earlier waits from before this feature are not reconstructed.

Tool approval is separate from the scan pause. On Continue, interrupted intents continue from their saved transcripts. The worker is told to repeat a call the operator approved, including when the approval was a standing grant for every tool in the task. That repeat uses the same tool and arguments. The approval click does not run the tool by itself; continuing the task lets the repeat run. A call that was not approved is not retried, and the wait is not a target response. Regex interception rules and the asset allowlist still apply over a standing grant.

Independent chat conversations retain their existing approval behavior.

## Authorization supplied by the user

A task created by the user carries the original prompt as prior authorization. Explicit hosts, IPs and CIDRs in that prompt receive operator scope grants, and manually selected targets or scope also count as authorization. Exact hostnames stay exact; they do not authorize the whole parent domain. Counterweight assumes the user completed the necessary due diligence for the requested work and passes the original limits to the planner, workers, and action reviewer.

A generic shell prompt no longer requests the same authorization on every command in such a task. It uses the configured action reviewer to assess actual effects. Explicit interception rules, exclusions, and action limits still apply. A request for genuinely additional permission uses the task-wide hold described above. Generated child-task text, discoveries, and agent scope proposals do not by themselves become user authorization.
