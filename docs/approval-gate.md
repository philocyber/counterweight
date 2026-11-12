# Approval gate and interception records

The global "Approval records" view, the in-task "Interception approval" panel, and the approval
cards shown inside conversations all expand to show full detail. The presentation follows the
[approval-detail component from AegisHook](https://github.com/RuoJi6/AegisHook/blob/main/web/src/components/CallDetail.vue),
reusing the upstream component and theme.

When shell interception and any restrictive tool-input rule are enabled, every
unmatched Bash/shell_open/shell_send execution requires human approval. This
includes literal commands, native programs, scripts and empty interactive input:
input parsing does not confine their filesystem or network effects. The model
judge cannot authorize this fallback; cancellation or the five-minute timeout
denies it even if the judge's timeout policy allows execution. Matching operator
allow/deny rules retain priority among rules of the same action, and a deny rule
always wins over an allow rule, whatever their priorities: an allow for a target
cannot permit one spelling of an operation that a deny rule covers. To permit an
operation a deny rule blocks, edit or disable that deny rule; the change is
explicit and visible. Disabling the selected tool's interception
remains an explicit operator choice. Repeated authorized research workflows can
use narrowly scoped operator allow rules. The syntax descriptions below concern
parsed data/code roles, rather than permission to execute automatically.

When shell interception and a restrictive tool-input rule are enabled, a command
that selects its executable through a variable or command substitution requires
human approval. This applies even when the fallback judge is disabled or would
allow it; cancellation and the five-minute approval timeout block execution.
Explicit matching allow rules retain their priority. Disabling interception
retains the operator's unrestricted behavior. This approval gate does not
provide an OS sandbox or inspect every generated program or external script.
The same human approval applies to curl/wget request methods that depend on
runtime option arguments, argument splitting/globbing, or explicit configuration
files (curl `-K`/`--config`, wget `--config`/`-e`/`--execute`). Quoted scalar
header/body values retain their data role. Default startup configuration also
requires approval unless explicitly disabled: use curl `-q` or `--disable` as
the first argument, or wget `--no-config` as an option. These flags do not exempt
explicit configuration or runtime method arguments from review. Windows
`curl.exe`/`wget.exe` names and explicit Windows paths receive the same HTTP
checks. Curl variable-expanded method/config options (`--expand-request` and
`--expand-config`) and prefixes of sensitive curl/wget long options also
require review, even when default configuration is disabled. Their presence
inside header/body data does not make that data a method selector.
Unsupported executors, shell state and runtime network enforcement
still need separate controls.
For bash/sh/dash/ksh, generated `-c` programs, external script files, unknown
stdin programs and unresolved invocation options also require human approval.
Literal programs remain inspectable, and known noninteractive syntax-check
modes retain their data behavior. Script files are not opened during review.
Literal `eval` arguments are joined and inspected as shell code. Generated
`eval` arguments and files loaded through `source`/`.` require human approval,
including supported `command`/`builtin` forms. Empty programs and printed text
retain their ordinary behavior. Literal trap actions are inspected as deferred
shell programs; generated actions require approval. Alias definitions are
inspected and also require approval when no earlier rule decides them, because
replacement fragments can combine with later arguments and shell state. Trap
listing/reset and alias listing/query forms remain data operations. Supported
`builtin trap` and `builtin alias` follow the same checks; persistent session
state is not reconstructed or sandboxed.
Incomplete or unsupported shell syntax also requires human approval when a
restrictive input rule is enabled, including uninspectable nested programs.
Quoted text, empty input and known noninteractive syntax-check programs remain
data; an explicit matching allow rule can still authorize execution.
