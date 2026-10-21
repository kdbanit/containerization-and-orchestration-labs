
#!/usr/bin/env bash
set -euo pipefail

CG=/sys/fs/cgroup/api

# Запускаем процесс внутри cgroup
CG="$CG" bash -c '
    echo "$BASHPID" > "$CG/cgroup.procs" || exit 1
    exec "$@"
' bash "$@" &

pid=$!

prev_throttled=$(awk '$1=="nr_throttled"{print $2}' "$CG/cpu.stat")
prev_periods=$(awk '$1=="nr_periods"{print $2}' "$CG/cpu.stat")
prev_oom=$(awk '$1=="oom_kill"{print $2}' "$CG/memory.events")

# Мониторинг в фоне
monitor() {
    while sleep 5; do
        throttled=$(awk '$1=="nr_throttled"{print $2}' "$CG/cpu.stat")
        periods=$(awk '$1=="nr_periods"{print $2}' "$CG/cpu.stat")
        oom=$(awk '$1=="oom_kill"{print $2}' "$CG/memory.events")

        dt=$((throttled - prev_throttled))
        dp=$((periods - prev_periods))

        if (( dp > 0 )); then
            pct=$((100 * dt / dp))
        else
            pct=0
        fi

        echo "CPU throttling: ${pct}% (+${dt} periods)"

        if (( oom > prev_oom )); then
            echo "OOM KILL detected!"
        fi

        prev_throttled=$throttled
        prev_periods=$periods
        prev_oom=$oom
    done
}

monitor &
monitor_pid=$!

# Ожидаем завершения сервиса
status=0
wait "$pid" || status=$?

# Останавливаем монитор
kill "$monitor_pid" 2>/dev/null || true
wait "$monitor_pid" 2>/dev/null || true

# Финальная проверка
oom=$(awk '$1=="oom_kill"{print $2}' "$CG/memory.events")

if (( oom > prev_oom )); then
    echo "OOM KILL detected! Total: $oom"
fi

exit "$status"
