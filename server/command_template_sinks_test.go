package server

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestCommandTemplateRejectsParameterEvaluationSinks(t *testing.T) {
	for _, program := range []string{
		// Arithmetic and variable-reference evaluation.
		`[[ {value} -gt 100 ]]`,
		`n={value}; if (( n > 100 )); then :; fi`,
		`n={value}; printf '%s' "$((n+1))"`,
		`n={value}; s=abcdef; printf '%s' "${s:0:n}"`,
		`n={value}; [[ $n -gt 0 ]]`,
		`n={value}; for ((i=0; i<n; i++)); do :; done`,
		`n={value}; let "x=n+1"`,
		`arr[{value}]=x`,
		`n={value}; arr[n]=x`,
		`[[ -v {value} ]]`,
		`test -v {value}`,
		`declare -i n; n={value}`,
		`declare -i n={value}`,
		`declare -n ref={value}; printf '%s' "$ref"`,
		`export {value}`,
		`readonly {value}`,
		`declare {value}`,
		`run() { local {value}; }; run`,
		`command declare {value}`,
		// Program text outside eval and the listed interpreters.
		`trap {value} EXIT`,
		`trap '{value}' EXIT`,
		`action={value}; trap "$action" EXIT`,
		`action={value}; trap 'eval "$action"' EXIT`,
		`mapfile -C {value} -c 1 lines < /dev/null`,
		`readarray -C '{value}' -c 1 lines < /dev/null`,
		`source <(printf '%s' {value})`,
		`. <(printf '%s' {value})`,
		`source /dev/stdin <<< {value}`,
		`printf '%s' {value} | . /dev/stdin`,
		`source "$(printf '%s' {value})"`,
		`bash /dev/stdin <<< {value}`,
		`bash -- /dev/stdin <<< {value}`,
		`bash /dev/fd/3 3<<< {value}`,
		`bash <(printf '%s' {value})`,
		`bash -- <(printf '%s' {value})`,
		`python3 /dev/stdin <<< {value}`,
		`python3 -- /dev/stdin <<< {value}`,
		// Other spellings of the same streams.
		`bash /dev//stdin <<< {value}`,
		`bash /dev/./fd/3 3<<< {value}`,
		`source /dev/../dev/stdin <<< {value}`,
		`python3 /dev//stdin <<< {value}`,
		`bash /proc/thread-self/fd/0 <<< {value}`,
		// Variables Bash fills without an assignment.
		`read -r program <<< {value}; eval "$program"`,
		`read -r <<< {value}; eval "$REPLY"`,
		`read -ra parts <<< {value}; eval "${parts[0]}"`,
		`mapfile -t lines <<< {value}; eval "${lines[0]}"`,
		`readarray -t <<< {value}; eval "${MAPFILE[0]}"`,
		`printf '%s\n' {value} | while read -r line; do sh -c "$line"; done`,
		`read -r program <<< {value}; bash -c "$program"`,
		`read -r program <<< {value}; printf '%s' "$program" | sh`,
		`[[ {value} =~ (.*) ]]; eval "${BASH_REMATCH[1]}"`,
		`: {value}; eval "$_"`,
		`printf '%s' {value} | { program=$(cat); eval "$program"; }`,
		`set -- {value}; for program; do eval "$program"; done`,
		// Positional parameters of a fixed child program.
		`bash -c 'eval "$1"' fixture {value}`,
		`sh -c 'eval "$1"' fixture {value}`,
		`data={value}; bash -c 'eval "$1"' fixture "$data"`,
		`bash -c 'sh -c "$1"' fixture {value}`,
		`bash -c 'eval "$@"' fixture {value}`,
		`bash -c 'eval "$0"' {value}`,
		`bash -c '[[ $1 -gt 0 ]]' fixture {value}`,
		`bash -c 'unset "$1"' fixture {value}`,
		`bash -s -- {value} <<< 'eval "$1"'`,
		// Interpreters started by programs outside the wrapper list.
		`printf '%s\n' a | xargs sh -c {value}`,
		`xargs sh -c 'echo {value}' < /dev/null`,
		`find . -maxdepth 0 -exec sh -c {value} \;`,
		`setsid bash -c {value}`,
		`stdbuf -o0 bash -c "echo {value}"`,
		`proxychains4 bash -c {value}`,
		`$SHELL -c {value}`,
		`"$SHELL" -c "echo {value}"`,
		`shell=bash; $shell -c {value}`,
		`xargs python3 -c {value} < /dev/null`,
		// A child program's own variables carry its arguments to what it evaluates.
		`bash -c 'n=$1; [[ $n -gt 5 ]] && echo big' fixture {value}`,
		`bash -c 'c=$1; for ((i=0;i<c;i++)); do :; done' fixture {value}`,
		`bash -c 'n=$1; echo $((n+1))' fixture {value}`,
		`sh -c 'n=$1; echo $((n+1))' fixture {value}`,
		`bash -c 'f() { local n=$1; echo $((n)); }; f "$1"' fixture {value}`,
		`bash -c 'for a; do n=$a; done; echo $((n))' fixture {value}`,
		`bash -c 'read -r n <<< "$1"; echo $((n))' fixture {value}`,
		`bash -c 'printf -v n %s "$1"; echo $((n))' fixture {value}`,
		`bash -c ': "${n:=$1}"; echo $((n))' fixture {value}`,
		`bash -c 's=abcdef; n=$1; m=$n; echo "${s:0:m}"' fixture {value}`,
		// Arithmetic in a child program or in eval text follows bare names.
		`N={value} bash -c 'echo $((N*2))'`,
		`N={value} bash -c '(( N > 10 )) && echo big'`,
		`export N={value}; bash -c 'M=$N; echo $((M))'`,
		`export N={value}; bash -c 'bash -c "echo \$((N))"'`,
		`x={value}; eval 'echo $((x))'`,
		`x={value}; eval '(( x > 1 ))'`,
		`x={value}; eval 'let x'`,
		`n={value}; trap '(( n > 0 )) && echo done' EXIT`,
		// A child program that takes its inherited stdin as a program.
		`printf '%s' {value} | bash -c 'read -r program; eval "$program"'`,
		`printf '%s' {value} | bash -c 'eval "$(cat)"'`,
		`printf '%s' {value} | bash -c 'bash'`,
		`bash -c 'exec sh' <<< {value}`,
		// Indirect expansion and prompt expansion of a value.
		`ref={value}; echo "${!ref}"`,
		`ref={value}; echo "${!ref:-fixed}"`,
		`run() { echo "${!1}"; }; run {value}`,
		`set -- {value}; echo "${!1}"`,
		`bash -c 'echo "${!1}"' fixture {value}`,
		`x={value}; echo "${x@P}"`,
		`run() { echo "${1@P}"; }; run {value}`,
		`bash -c 'echo "${1@P}"' fixture {value}`,
		`bash -c 'x=$1; echo "${x@P}"' fixture {value}`,
		// Assignments made by an expansion.
		`x={value}; : "${y:=$x}"; echo $(( y ))`,
		`x={value}; : "${y=$x}"; echo $(( y ))`,
		`x={value}; : "${y:=$x}"; eval "$y"`,
		// Integer variables: Bash's own, and attributes set through a builtin call.
		`RANDOM={value}; shuf -n1 list.txt`,
		`OPTIND={value}`,
		`HISTCMD={value}`,
		`SRANDOM={value}`,
		`x={value}; RANDOM=x`,
		`read -r RANDOM <<< {value}`,
		`bash -c 'RANDOM=$1' fixture {value}`,
		`x={value}; declare -i y; y=x`,
		`declare -i total=0; n={value}; total+=n`,
		`command declare -i n; n={value}`,
		`command typeset -i n=0; n={value}`,
		`builtin declare -n ref; ref={value}; printf '%s' "$ref"`,
		`\declare -i n; n={value}`,
		`X=1 declare -i n; n={value}`,
		`run() { command local -i n; n=$1; }; run {value}`,
		`x={value}; command declare -i n=x`,
		// Arrays that are not known to be associative.
		`f(){ local -A m; }; i={value}; m[$i]=1`,
		`f(){ local -A m; }; m[{value}]=1`,
		`m=(0); declare -A m 2>/dev/null; i={value}; echo "${m[$i]}"`,
		`if false; then declare -A m; fi; i={value}; m[$i]=1`,
		`( declare -A m ); i={value}; m[$i]=1`,
		`declare -A m & i={value}; m[$i]=1`,
		`declare -pA m; i={value}; m[$i]=1`,
		`declare -A m; unset m; i={value}; m[$i]=1`,
		`declare -A m; f() { local m; m[$1]=1; }; f {value}`,
		`i={value}; m[$i]=1; declare -A m`,
		`declare -A m; bash -c 'm[$1]=1' fixture {value}`,
		`declare -A GROUPS; i={value}; : "${GROUPS[$i]}"`,
		// Operands a builtin can take as an option that names a variable.
		`test {value} {value}`,
		`[ {value} {value} ]`,
		`test ! {value} {value}`,
		`test -n fixed -a {value} {value}`,
		`op={value}; name={value}; test "$op" "$name"`,
		`sleep 0 & wait -n {value}`,
		`sleep 0 & wait -n -p {value}`,
		`sleep 0 & wait -p {value} $!`,
		`sleep 0 & wait -fp {value} $!`,
		// Variables that Bash, or a shell it starts, expands again.
		`PS4={value}; set -x; true`,
		`x={value}; PS4="+$x "; set -x; true`,
		`read -r PS4 <<< {value}; set -x; true`,
		`bash -c 'PS4=$1; set -x; true' fixture {value}`,
		`BASH_ENV={value} bash ./run.sh`,
		`export BASH_ENV={value}; bash ./run.sh`,
		`ENV={value} sh -i`,
		`PROMPT_COMMAND={value} bash -i`,
		`PS1={value} bash -i`,
		// A child program this grammar cannot read.
		`sh -c 'function; eval "$1"' fixture {value}`,
		`sh -c 'echo "$1" (' fixture {value}`,
		// Launchers and builtins that expand their input again.
		`printf '%s\n' {value} | xargs -I% sh -c 'echo %'`,
		`printf '%s\n' {value} | xargs -i sh -c 'echo {}'`,
		`printf '%s\n' {value} | xargs --replace=% sh -c 'echo %'`,
		`printf '%s\n' {value} | xargs -rI% bash -c 'echo %'`,
		`data={value}; xargs -I% sh -c 'echo %' <<< "$data"`,
		`xargs -I% sh -c 'echo %' {value} < /dev/null`,
		`printf '%s\n' fixed | xargs {value} sh -c 'echo %'`,
		`printf '%s\n' {value} | xargs sh -c`,
		`printf '%s\n' {value} | xargs -n1 sh -c 'eval "$1"' fixture`,
		`printf '%s\n' {value} | xargs -0 bash -c -x`,
		`printf '%s\n' {value} | xargs python3 -c`,
		`compgen -W {value} -- fixed`,
		`compgen -C {value}`,
		// Arithmetic follows the fixed text a variable holds to the names in it.
		`n={value}; m=n; echo $((m))`,
		`a={value}; b=a; c=b; echo $((c))`,
		`a={value}; b=a; c=$b; echo $((c))`,
		`n={value}; m='n+1'; echo $((m))`,
		`n={value}; m=n; echo $(($m))`,
		`n={value}; m=n; (( m > 1 ))`,
		`n={value}; m=n; let m`,
		`n={value}; m=n; for ((i=0; i<m; i++)); do :; done`,
		`n={value}; m=n; arr[m]=x`,
		`n={value}; m=n; s=abcdef; printf '%s' "${s:m}"`,
		`n={value}; m=n; [[ $m -eq 1 ]]`,
		`n={value}; m=n; declare -i x; x=m`,
		`n={value}; arr=(n); echo $((arr[0]))`,
		`n={value}; for m in n; do echo $((m)); done`,
		`n={value}; printf -v m '%s' n; echo $((m))`,
		`n={value}; : "${m:=n}"; echo $((m))`,
		`n={value}; eval 'm=n'; echo $((m))`,
		`N={value} bash -c 'M=N; echo $((M))'`,
		`N={value} bash -c 'A=N; B=A; echo $((B+1))'`,
		`export N={value} M=N; bash -c 'echo $((M))'`,
		`run() { echo $(($1)); }; n={value}; run n`,
		`outer() { run "$1"; }; run() { echo $(($1)); }; n={value}; outer n`,
		`n={value}; set -- n; echo $(($1))`,
		`run() { eval 'echo $(($1))'; }; n={value}; run n`,
		`n={value}; set -- n; eval 'echo $(($1))'`,
		`n={value}; trap 'echo $(($1))' EXIT; set -- n`,
		`export n={value}; bash -c 'echo $(($1))' fixture n`,
		`n={value}; command let n`,
		`n={value}; arr=(x); unset 'arr[n]'`,
		`n={value}; printf -v 'arr[n]' '%s' x`,
		`n={value}; arr=(x); [[ -v arr[n] ]]`,
		// Attributes declared by fixed eval text.
		`eval 'declare -i n'; n={value}`,
		`eval declare -i n; n={value}`,
		`command eval 'declare -i n'; n={value}`,
		`eval 'eval "declare -i n"'; n={value}`,
		`eval 'declare -n ref'; ref={value}; printf '%s' "$ref"`,
		`run() { eval 'declare -gi n'; }; run; n={value}`,
		`eval 'typeset -i y'; x={value}; y=x`,
		`eval 'declare -A m'; i={value}; m[$i]=1`,
		`i={value}; m=(0); eval 'declare -A m 2>/dev/null; m[i]=1'`,
		// eval text spelled through variables that hold one fixed value.
		`decl='declare -i n'; eval "$decl"; n={value}`,
		`decl='declare -i n'; eval $decl; n={value}`,
		`name=n; eval "declare -i $name"; n={value}`,
		`name=n; decl="declare -i $name"; eval "$decl"; n={value}`,
		`export decl='declare -i n'; eval "$decl"; n={value}`,
		`decl='declare -n ref'; eval "$decl"; ref={value}; printf '%s' "$ref"`,
		`inner='declare -i n'; outer='eval "$inner"'; eval "$outer"; n={value}`,
		`decl='declare -i n'; run() { eval "$decl"; }; run; n={value}`,
		`n={value}; cmd='(( n > 1 ))'; eval "$cmd"`,
		`n={value}; cmd='m=n'; eval "$cmd"; echo $((m))`,
		// Fixed trap text declares and assigns like eval text, whatever the condition.
		`trap 'declare -i n' DEBUG; :; n={value}`,
		`trap 'declare -i n' ERR; false; n={value}`,
		`trap 'declare -i n' RETURN; n={value}`,
		`trap 'declare -i n' EXIT; n={value}`,
		`trap 'declare -i n' USR1; kill -USR1 $$; n={value}`,
		`trap -- 'declare -i n' DEBUG; :; n={value}`,
		`command trap 'declare -i n' DEBUG; :; n={value}`,
		`run() { trap 'local -i n' DEBUG; :; n=$1; }; run {value}`,
		`action='declare -i n'; trap "$action" DEBUG; :; n={value}`,
		`trap 'eval "declare -i n"' DEBUG; :; n={value}`,
		`n={value}; trap 'm=n' DEBUG; :; echo $((m))`,
		// After eval or trap text that is not fixed, a variable can carry any attribute.
		`eval "$(cat setup.sh)"; n={value}`,
		`eval "$(cat setup.sh)"; read -r n <<< {value}`,
		`eval "$(cat setup.sh)"; printf -v n '%s' {value}`,
		`eval "$(cat setup.sh)"; for n in {value}; do :; done`,
		`n={value}; eval "$(cat setup.sh)"; n+=1`,
		`run() { n=$1; }; eval "$(cat setup.sh)"; run {value}`,
		`for i in 1 2; do n={value}; eval "$(cat setup.sh)"; done`,
		`set -- 'declare -i n'; eval "$1"; n={value}`,
		`trap "$(cat handler.sh)" DEBUG; :; n={value}`,
		// A variable that is not known to hold one fixed value.
		`decl='echo hi'; change() { decl='declare -i n'; }; change; eval "$decl"; n={value}`,
		`if false; then decl='echo hi'; fi; eval "$decl"; n={value}`,
		`decl='echo hi'; decl='declare -i n'; eval "$decl"; n={value}`,
		`decl='echo hi'; printf -vdecl '%s' 'declare -i n'; eval "$decl"; n={value}`,
		`decl='echo hi'; eval 'decl="declare -i n"'; eval "$decl"; n={value}`,
		`run() { eval "$decl"; }; decl='declare -i n'; run; n={value}`,
		// A word the shell splits at blanks, expands braces in, or splits on
		// another IFS is not the text its parts spell.
		`s=' '; decl='declare -i'; eval $decl${s}n; n={value}`,
		`eval {"declare -i ",}n; n={value}`,
		`decl='declare -i n'; noop=true; eval {"$decl","$noop"}';'; n={value}`,
		`a=IF; b=S=x; eval "$a$b"; decl='declare -ixn'; eval $decl; n={value}`,
		// Text this grammar cannot read may still run.
		`eval 'declare -i n; : ${n@Z}' 2> /dev/null; n={value}`,
		`trap 'declare -i n; : ${n@Z}' DEBUG; :; n={value}`,
		// A mapfile callback is program text like eval text, and its last
		// command receives the input lines.
		`mapfile -C 'declare -i n #' -c 1 lines <<< x; n={value}`,
		`readarray -C 'declare -i n #' -c 1 lines <<< x; n={value}`,
		`mapfile '-Cdeclare -i n #' -c 1 lines <<< x; n={value}`,
		`mapfile -tC 'declare -i n #' -c 1 lines <<< x; n={value}`,
		`command mapfile -c 1 -C 'declare -i n #' lines <<< x; n={value}`,
		`n={value}; mapfile -C 'm=n #' -c 1 lines <<< x; echo $((m))`,
		`n={value}; mapfile -C '(( n > 1 )) #' -c 1 lines <<< x`,
		`n={value}; mapfile '-C(( n > 1 )) #' -c 1 lines <<< x`,
		`n={value}; callback='(( n > 1 )) #'; mapfile -C "$callback" -c 1 lines <<< x`,
		`callback='declare -i n #'; mapfile -C "$callback" -c 1 lines <<< x; n={value}`,
		`mapfile -C "$(cat callback.sh)" -c 1 lines <<< x; n={value}`,
		`option=-C; mapfile "$option" 'declare -i n #' -c 1 lines <<< x; n={value}`,
		`mapfile -C 'echo {value} #' -c 1 lines <<< x`,
		`callback={value}; mapfile -C "$callback" -c 1 lines <<< x`,
		`run() { eval "$2"; }; mapfile -C run -c 1 lines <<< {value}`,
		// A write to a name that is not fixed text can change any variable,
		// so no variable spells known eval text any more.
		`t=de; decl='echo hi'; printf -v "${t}cl" '%s' 'declare -i n'; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; read -r "${t}cl" <<< 'declare -i n'; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; mapfile -t "${t}cl" <<< 'declare -i n'; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; declare "${t}cl=declare -i n"; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; export "${t}cl=declare -i n"; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; declare -n ref="${t}cl"; ref='declare -i n'; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; eval "${t}cl='declare -i n'"; eval "$decl"; n={value}`,
		`t=de; decl='echo hi'; eval 'printf -v "${t}cl" %s "declare -i n"'; eval "$decl"; n={value}`,
		// A declaration that gives the integer or nameref attribute to a name,
		// or through an option, that is not a plain fixed word.
		`name=n; declare -i "$name"; n={value}`,
		`flag=-i; declare "$flag" n; n={value}`,
		`flag=-i; name=n; declare "$flag" "$name"; n={value}`,
		`declare -i "n"; n={value}`,
		`name=n; typeset -ai "$name"; n={value}`,
		`name=ref; declare -n "$name"; ref={value}; printf '%s' "$ref"`,
		`name=n; command declare -i "$name"; n={value}`,
		`run() { local name=n; local -i "$name"; n=$1; }; run {value}`,
		`name=n; eval 'declare -i "$name"'; n={value}`,
		`name=n; trap 'declare -i "$name"' DEBUG; :; n={value}`,
		`x={value}; name=n; declare -i "$name"; n=x`,
		// When that name or option is not fixed, any variable may carry the attribute.
		`declare -i "$(cat name.txt)"; n={value}`,
		`declare "$(cat flag.txt)" n; m={value}`,
		`set -- n; declare -i "$1"; n={value}`,
		`declare -i "$(cat name.txt)"; read -r n <<< {value}`,
		`declare -i "$(cat name.txt)"; printf -v n '%s' {value}`,
		`declare -i "$(cat name.txt)"; for n in {value}; do :; done`,
		`n={value}; declare -i "$(cat name.txt)"; n+=1`,
		`x={value}; declare -i "$(cat name.txt)"; n=x`,
	} {
		t.Run(program, func(t *testing.T) {
			if rendered, err := renderBashCommandTemplate(program, map[string]any{"value": "printf SW_SINK_OUTSIDE"}); err == nil {
				t.Fatalf("parameter reached an evaluation sink: %s", rendered)
			}
		})
	}
}

// An interpreter name that is only an argument of another program is data.
func TestCommandTemplateInterpreterNamesAsArguments(t *testing.T) {
	for _, program := range []string{
		`curl -s {value} | grep -i python`,
		`grep -c python {value}`,
		`ps aux | grep sh | grep {value}`,
		`searchsploit bash {value}`,
		`printf '%s\n' {value} | xargs -n1 sh -c 'echo "$1"' fixture`,
		`printf '%s\n' {value} | xargs -I% echo %`,
		`find . -name {value} | xargs -I% cp % /tmp/out`,
		`mapfile -C 'echo row' -c 1 lines <<< x; printf '%s' {value}`,
	} {
		if _, err := renderBashCommandTemplate(program, map[string]any{"value": "fixture"}); err != nil {
			t.Errorf("%s was refused: %v", program, err)
		}
	}
}

func TestCommandTemplateFixedEvaluationWorkflowsRemainLiteral(t *testing.T) {
	bash := parameterFlowBash(t)
	value := `quotes ' " $(printf SW_SINK_OUTSIDE); {other}`
	for _, program := range []string{
		`bash -c 'printf "%s" "$1"' fixture {value}`,
		`read -r data <<< {value}; printf '%s' "$data"`,
		`data={value}; [[ -n $data ]] && printf '%s' "$data"`,
		`data={value}; [[ $data == quotes* ]] && printf '%s' "$data"`,
		`data={value}; if (( ${#data} > 0 )); then printf '%s' "$data"; fi`,
		`trap 'printf done > /dev/null' EXIT; printf '%s' {value}`,
		`count=3; for ((i=0; i<count; i++)); do :; done; printf '%s' {value}`,
		`n=5; (( n > 3 )) && printf '%s' {value}`,
		`declare -A map; key={value}; map[$key]=1; for key in "${!map[@]}"; do printf '%s' "$key"; done`,
		`run() { local data=$1; export data; printf '%s' "$data"; }; run {value}`,
		`find . -maxdepth 0 -exec sh -c 'printf "%s" "$1"' fixture {value} \;`,
		`source /dev/null; printf '%s' {value}`,
		`bash -- /dev/null; printf '%s' {value}`,
		`printf '%s' {value} | bash -c 'cat'`,
		// A child program that only stores and prints what it is given.
		`N={value} bash -c 'printf "%s" "$N"'`,
		`export N={value}; bash -c 'M=$N; printf "%s" "$M"'`,
		`bash -c 'n=$1; printf "%s" "$n"' fixture {value}`,
		`bash -c 'count=3; for ((i=0; i<count; i++)); do :; done; printf "%s" "$1"' fixture {value}`,
		`printf '%s' {value} | bash -c 'data=$(cat); printf "%s" "$data"'`,
		// Expansions that read a value without evaluating it.
		`ref=fixed; fixed=; printf '%s' "${!ref}" {value}`,
		`list=(a b); : "${!list[@]}" "${!li*}"; printf '%s' {value}`,
		`x={value}; : "${x@Q}" "${x@U}" "${x@E}" "${x@A}" "${x@a}"; printf '%s' "$x"`,
		`x={value}; : "${y:=$x}"; printf '%s' "$y"`,
		// Variables of Bash's own that hold fixed text, or that it does not evaluate.
		`SECONDS=0; RANDOM=7; OPTIND=1; printf '%s' {value}`,
		`PS4='+ '; printf '%s' {value}`,
		`IFS={value}; printf '%s' "$IFS"`,
		`command declare -x data; data={value}; printf '%s' "$data"`,
		`declare -i count=0; count+=1; printf '%s' {value}`,
		// A declared associative array keeps its keys as text.
		`declare -A seen; key={value}; seen[$key]=1; printf '%s' "${!seen[@]}"`,
		`declare -A seen; mark() { seen[$1]=1; }; mark {value}; printf '%s' "${!seen[@]}"`,
		`typeset -A seen=(); key={value}; seen[$key]=1; printf 'seen: %s' > /dev/null; printf '%s' "${!seen[@]}"`,
		// Comparisons as text and operands behind fixed operators.
		`data={value}; test "$data" = "$data" && printf '%s' "$data"`,
		`[ -n {value} ] && [ {value} != fixed ] && printf '%s' {value}`,
		`sleep 0 & pid=$!; wait "$pid"; printf '%s' {value}`,
		`sleep 0 & wait -- $!; printf '%s' {value}`,
		// Launchers and builtins that receive the value as an argument only.
		`printf '%s' {value} | xargs -0 printf '%s'`,
		`compgen -W 'alpha beta' -- {value}; printf '%s' {value}`,
		`x={value}; printf '%s' 'cost $5 (' > /dev/null; printf '%s' "$x"`,
		// Fixed text that names a variable is data until it reaches arithmetic.
		`m=n; echo "$m" > /dev/null; printf '%s' {value}`,
		`n={value}; m=n; printf '%s' "$m" > /dev/null; printf '%s' "$n"`,
		`n={value}; name=5; m=name; : $((m)); printf '%s' "$n"`,
		`n={value}; label='n is the value'; total=$((2+3)); : "$label $total"; printf '%s' "$n"`,
		`n={value}; count=0; for word in n other; do count=$((count+1)); done; printf '%s' "$n"`,
		`N={value} bash -c 'M=N; : "$M"; i=0; max=2; while (( i < max )); do i=$((i+1)); done; printf "%s" "$N"'`,
		`run() { : $(($1 + 1)); }; run 5; printf '%s' {value}`,
		`host={value}; printf '[host] %s' "$host" > /dev/null; printf '%s' "$host"`,
		`i=0; arr=(x); unset 'arr[i]'; printf '%s' {value}`,
		// eval text that declares nothing Bash evaluates on assignment.
		`eval 'declare -x n'; n={value}; printf '%s' "$n"`,
		`eval 'declare -i count=0'; count+=1; printf '%s' {value}`,
		// eval and trap text that variables with one fixed value spell out.
		`cmd='echo hi'; eval "$cmd" > /dev/null; x={value}; printf '%s' "$x"`,
		`cmd='echo hi'; eval $cmd > /dev/null; x={value}; printf '%s' "$x"`,
		`opts='-n'; cmd="echo $opts hi"; eval "$cmd" > /dev/null; x={value}; printf '%s' "$x"`,
		`finish='printf done > /dev/null'; trap "$finish" EXIT; x={value}; printf '%s' "$x"`,
		`trap 'echo done > /dev/null' EXIT; printf '%s' {value}`,
		`trap 'declare -x n' DEBUG; n={value}; trap - DEBUG; printf '%s' "$n"`,
		`trap - EXIT; trap '' INT; trap -p EXIT; x={value}; printf '%s' "$x"`,
		`PROMPT_COMMAND='declare -i n'; n={value}; printf '%s' "$n"`,
		// Text that is not fixed leaves parameters usable as arguments, and in
		// variables stored before it runs.
		`eval "$(printf 'echo hi')" > /dev/null; printf '%s' {value}`,
		`x={value}; eval "$(printf 'echo hi')" > /dev/null; printf '%s' "$x"`,
		`eval "$(printf 'echo hi')" > /dev/null; while read -r line; do :; done < /dev/null; printf '%s' {value}`,
		`tmp=$(mktemp); trap "rm -f $tmp" EXIT; printf '%s' {value} > "$tmp"; cat "$tmp"`,
		`eval ': ${n@Z}' 2> /dev/null; printf '%s' {value}`,
		// mapfile without a callback, and a fixed callback that only prints.
		`mapfile -t lines <<< {value}; printf '%s' "${lines[0]}"`,
		`mapfile -t -C 'echo row > /dev/null #' -c 1 lines <<< x; printf '%s' {value}`,
		`mapfile -C ': row' -c 1 lines <<< x; x={value}; printf '%s' "$x"`,
		`printf -v out '%s' {value}; printf '%s' "$out"`,
		// Writes to fixed names leave a variable that spells eval text known.
		`cmd='echo hi'; eval "$cmd" > /dev/null; printf -v out '%s' fixed; export "MODE=$out"; x={value}; printf '%s' "$x"`,
		`cmd='echo hi'; eval "$cmd" > /dev/null; items=(a b); i=1; (( items[$i] = 2 )); let "i=i+1"; x={value}; printf '%s' "$x"`,
		// Declarations that name a variable indirectly without an attribute
		// Bash evaluates, or with parameters only as arguments or stored before.
		`name=out; declare "$name"=fixed; printf '%s' {value}`,
		`name=out; declare -r "$name"; printf '%s' {value}`,
		`name=out; declare "$name"=fixed; x={value}; printf '%s' "$x"`,
		`name=count; declare -i "$name"; count=3; x={value}; printf '%s' "$x"`,
		`declare -r "$(printf out)"; printf '%s' {value}`,
		`x={value}; declare -i "$(printf count)"; printf '%s' "$x"`,
		`export "MODE=$HOME"; x={value}; printf '%s' "$x"`,
	} {
		t.Run(program, func(t *testing.T) {
			rendered, err := renderBashCommandTemplate(program, map[string]any{"value": value, "other": "changed"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", rendered)
			hideTemplateParserWindow(cmd)
			output, err := cmd.CombinedOutput()
			if err != nil || string(output) != value {
				t.Fatalf("literal workflow changed: output=%q want=%q error=%v", output, value, err)
			}
		})
	}
}
