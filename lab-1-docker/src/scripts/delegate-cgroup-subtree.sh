#!/bin/bash
set -euo pipefail

UID_TO_DELEGATE=1000
GID_TO_DELEGATE=1000
ROOT=/sys/fs/cgroup
CG="$ROOT/user-$UID_TO_DELEGATE"

# Включаем контроллеры для дочерних cgroup.
# Оставляем только реально доступные.
controllers=""
for c in cpu memory pids; do
    if grep -qw "$c" "$ROOT/cgroup.controllers"; then
        controllers="$controllers +$c"
    fi
done

if [ -n "$controllers" ]; then
    echo "$controllers" > "$ROOT/cgroup.subtree_control"
fi

# Создаём корень делегированного поддерева.
mkdir -p "$CG"

# Пользователь должен иметь возможность создавать дочерние cgroup.
chown "$UID_TO_DELEGATE:$GID_TO_DELEGATE" "$CG"

# Файлы, необходимые для управления деревом.
for f in cgroup.procs cgroup.threads cgroup.subtree_control; do
    if [ -e "$CG/$f" ]; then
        chown "$UID_TO_DELEGATE:$GID_TO_DELEGATE" "$CG/$f"
    fi
done

echo "Delegated: $CG"
cat "$CG/cgroup.controllers"