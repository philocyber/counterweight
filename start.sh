#!/bin/sh
# Counterweight launcher: stop an earlier Counterweight on the listen ports (never
# another program), then run the restart loop built into the
# binary (`counterweight -supervise`: exit 0 stops, 75 restarts immediately, other codes
# back off). Download verification and atomic replacement are handled by selfupdate.Bootstrap.
set -u
msg(){ printf '%s' "$1"; }


cd "$(dirname "$0")" || exit 1

BIN=./counterweight
[ -x "$BIN" ] || { echo "[Counterweight] $(msg "Executable not found: $BIN")" >&2; exit 1; }

# Ports this process is about to bind. An empty proxy address disables that listener.
listen_ports() {
	mgmt_addr=127.0.0.1:8787
	proxy_addr=127.0.0.1:8788
	flag_name=
	for arg in "$@"; do
		if [ -n "$flag_name" ]; then
			case "$flag_name" in
				addr) mgmt_addr=$arg ;;
				proxy) proxy_addr=$arg ;;
			esac
			flag_name=
			continue
		fi
		case "$arg" in
			-addr|--addr) flag_name=addr ;;
			-proxy|--proxy) flag_name=proxy ;;
			-addr=*|--addr=*) mgmt_addr=${arg#*=} ;;
			-proxy=*|--proxy=*) proxy_addr=${arg#*=} ;;
		esac
	done
	printf '%s\n' "$mgmt_addr" | awk -F: 'NF { print $NF }'
	if [ -n "$proxy_addr" ]; then
		printf '%s\n' "$proxy_addr" | awk -F: 'NF { print $NF }'
	fi
}

listen_pids() {
	if ! command -v lsof >/dev/null 2>&1; then
		if [ "${lsof_warned:-0}" -eq 0 ]; then
			echo "[Counterweight] $(msg "lsof is required to close a busy listen port")" >&2
			lsof_warned=1
		fi
		return 0
	fi
	lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | awk 'NF && !seen[$1]++ { print $1 }'
}

# True when the process is a Counterweight binary, judged by its executable name
# (not by its arguments or working directory, which another program can share).
is_counterweight() {
	cw_comm=$(ps -o comm= -p "$1" 2>/dev/null | sed 's/[[:space:]]*$//')
	[ "${cw_comm##*/}" = counterweight ]
}

stop_listener() {
	port_pid=$1
	case "$port_pid" in
		''|*[!0-9]*|0|1|$$) return ;;
	esac
	port_cmd=$(ps -o command= -p "$port_pid" 2>/dev/null || true)
	# Only an earlier Counterweight may be stopped. Any other program keeps its port,
	# and nothing about it (including its parent) is signalled.
	if ! is_counterweight "$port_pid"; then
		echo "[Counterweight] $(msg "Port $2 is in use by another program (pid $port_pid: $port_cmd). Stop it, or choose other ports: ./start.sh -addr 127.0.0.1:9000 -proxy 127.0.0.1:9001")" >&2
		exit 1
	fi
	port_ppid=$(ps -o ppid= -p "$port_pid" 2>/dev/null | tr -d '[:space:]')
	case "$port_ppid" in
		''|*[!0-9]*|0|1|$$) ;;
		*)
			port_pcmd=$(ps -o command= -p "$port_ppid" 2>/dev/null || true)
			case "$port_pcmd" in
				*[\ /]start.sh|*[\ /]start.sh\ *|*counterweight\ -supervise|*counterweight\ -supervise\ *)
					echo "[Counterweight] $(msg "Stopping supervisor $port_ppid so it does not relaunch")" >&2
					kill -TERM "$port_ppid" 2>/dev/null || true
					;;
			esac
			;;
	esac
	echo "[Counterweight] $(msg "Port $2 is open (pid $port_pid: $port_cmd); closing it")" >&2
	kill -TERM "$port_pid" 2>/dev/null || true
}

release_port() {
	rel_port=$1
	case "$rel_port" in
		''|*[!0-9]*) return ;;
	esac
	rel_pids=$(listen_pids "$rel_port")
	[ -n "$rel_pids" ] || return 0
	for port_pid in $rel_pids; do
		stop_listener "$port_pid" "$rel_port"
	done
	rel_n=0
	while [ "$rel_n" -lt 5 ]; do
		rel_left=$(listen_pids "$rel_port")
		[ -z "$rel_left" ] && return 0
		sleep 1
		rel_n=$((rel_n + 1))
	done
	for port_pid in $rel_left; do
		case "$port_pid" in
			''|*[!0-9]*|0|1|$$) continue ;;
		esac
		is_counterweight "$port_pid" || continue
		echo "[Counterweight] $(msg "Port $rel_port is still open (pid $port_pid); forcing it closed")" >&2
		kill -KILL "$port_pid" 2>/dev/null || true
	done
	sleep 1
}

release_listen_ports() {
	rel_ports=$(listen_ports "$@")
	for rel_port in $rel_ports; do
		release_port "$rel_port"
	done
}

release_listen_ports "$@"
exec "$BIN" -supervise "$@"
