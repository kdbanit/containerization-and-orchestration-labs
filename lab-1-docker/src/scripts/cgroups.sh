#!/usr/bin/env bash
set -euo pipefail

ROOT=/sys/fs/cgroup
CG=$ROOT/api
PCG=$ROOT/pids-test
URL=http://127.0.0.1:8080
MONITOR_PID=

stop_api() {
    echo 1 > "$CG/cgroup.kill" 2>/dev/null || true
    if [[ -n "$MONITOR_PID" ]]; then
        wait "$MONITOR_PID" 2>/dev/null || true
        MONITOR_PID=
    fi
}

cleanup() {
    trap - EXIT
    [[ -d "$PCG" ]] && {
        echo 1 > "$PCG/cgroup.kill" 2>/dev/null || true
        rmdir "$PCG" 2>/dev/null || true
    }
    [[ -d "$CG" ]] && {
        stop_api
        rmdir "$CG" 2>/dev/null || true
    }
    rm -f bin/api
}
trap cleanup EXIT

stat_value() {
    awk -v key="$2" '$1 == key {print $2}' "$1"
}

start_api() {
    bash monitor.sh bin/api >/dev/null 2>&1 &
    MONITOR_PID=$!

    for _ in {1..50}; do
        if curl -fsS --max-time 1 "$URL/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.1
    done
    echo "API failed to start" >&2
    exit 1
}

echo "+cpu +memory +pids" > "$ROOT/cgroup.subtree_control"
mkdir "$CG"
echo "50000 100000" > "$CG/cpu.max"
echo $((128 * 1024 * 1024)) > "$CG/memory.max"
echo 0 > "$CG/memory.swap.max"
echo 64 > "$CG/pids.max"
echo 1 > "$CG/memory.oom.group"

mkdir -p bin
go -C src/api build -o ../../bin/api

# CPU
start_api
before=$(stat_value "$CG/cpu.stat" nr_throttled)
curl -sS --max-time 5 "$URL/burn" >/dev/null 2>&1 || true
after=$(stat_value "$CG/cpu.stat" nr_throttled)
echo "CPU throttling: $((after - before)) periods"

# Останавливаем бесконечный /burn и запускаем API заново
stop_api
echo "max 100000" > "$CG/cpu.max"
start_api

# Тесты PIDs в отдельной cgroup
mkdir "$PCG"
echo 5 > "$PCG/pids.max"
before=$(stat_value "$PCG/pids.events" max)

timeout -k 2s 5s bash -c '
    echo "$BASHPID" > "$1/cgroup.procs"
    exec stress-ng --fork 7 --timeout 2s
' bash "$PCG" >/dev/null 2>&1 || true

after=$(stat_value "$PCG/pids.events" max)
echo "PIDs limit hits: $((after - before))"
echo 1 > "$PCG/cgroup.kill"
rmdir "$PCG"

# Тесты OOM
before=$(stat_value "$CG/memory.events" oom_kill)
curl -sS --max-time 10 "$URL/eat?mb=256" >/dev/null 2>&1 || true
after=$(stat_value "$CG/memory.events" oom_kill)
echo "OOM kills: $((after - before))"