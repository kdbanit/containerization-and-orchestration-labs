#!/usr/bin/env bash
set -euo pipefail

# Проверяем запуск с sudo, чтобы отдать UID и GID запустившего пользователя без привилегий
if [[ -z "${SUDO_UID:-}" || -z "${SUDO_GID:-}" ]]; then
    echo "Error: run via sudo" >&2
    exit 1
fi

HOST_UID=$SUDO_UID
HOST_GID=$SUDO_GID

# Генерируем UID контейнера
CONTAINER_UID=$(uuidgen | head -c 8)
SERVICE_NAME=api
CONTAINER_NAME="mydocker-$SERVICE_NAME-$CONTAINER_UID"
ROOT=/sys/fs/cgroup
CG="$ROOT/$CONTAINER_NAME"

# Выдаём на корень возможность контролировать в subtree pids, memory и cpu, если их нет
required=(cpu memory pids)

for controller in "${required[@]}"; do
    if ! grep -qw "$controller" "$ROOT/cgroup.subtree_control"; then
        echo "+$controller" > "$ROOT/cgroup.subtree_control"
    fi
done

# Создаём cgroup
mkdir -p "$CG"

# Производим graceful shutdown через перехват выхода и cleanup
cleanup() {
    echo "Cleaning up: $CG"
    rmdir "$CG"
}

trap cleanup EXIT

# Устанавливаем лимиты на потоки и память
echo "50000 100000" > "$CG/cpu.max"
echo $((128 * 1024 * 1024)) > "$CG/memory.max"
# Отключаем swap
echo 0 > "$CG/memory.swap.max"
# 64 PID'а на cgroup
echo 64 > "$CG/pids.max"
# Убиваем всю cgroup
echo 1 > "$CG/memory.oom.group"

# Запускаем subshell и прокидываем в него UID и GID запустившего пользователя и cgroup
# Дальше помещаем subshell в cgroup
# После запускаем процесс в namespace'ах
# Черзе наш лаунчер для сброса capabilities и запрета syscall getppid
CG="$CG" HOST_UID="$HOST_UID" HOST_GID="$HOST_GID" CONTAINER_NAME="$CONTAINER_NAME" bash -c '
    echo "$BASHPID" > "$CG/cgroup.procs"

    CONTAINER_NAME="$CONTAINER_NAME" exec unshare \
        --user \
        --map-users="0:${HOST_UID}:1" \
        --map-groups="0:${HOST_GID}:1" \
        --setuid=0 \
        --setgid=0 \
        --pid --mount --net --uts --ipc \
        --fork --mount-proc \
        /bin/bash -c "
            ip link set lo up
            hostname "$CONTAINER_NAME"
            exec /opt/mydocker/launcher /opt/mydocker/api &
        "
'