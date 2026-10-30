#!/usr/bin/bash
# Lab 5, Part 5 — live access-matrix check.
#
# Every shop workload is checked against every other shop workload that
# actually listens (api:80, postgres:5432). Sources from other namespaces are
# checked only against api. The client is created with `kubectl run`.
set -uo pipefail

# exec_in <namespace> <pod-selector> <command...>  -> "allow" | "deny"
exec_in() {
  local ns="$1" selector="$2"; shift 2
  local pod
  pod="$(kubectl -n "$ns" get pods -l "$selector" --field-selector=status.phase=Running \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
  kubectl -n "$ns" exec "$pod" -- "$@" >/dev/null 2>&1 && echo allow || echo deny
}

# run_once <namespace> <labels|-> <command...>  -> "allow" | "deny"
run_once() {
  local ns="$1" labels="$2"; shift 2
  local pod="test-$RANDOM" rc=0
  local args=(-n "$ns" run "$pod" --image=busybox --restart=Never -i)
  [ "$labels" != "-" ] && args+=(--labels "$labels")
  args+=(--command -- "$@")
  kubectl "${args[@]}" >/dev/null 2>&1 || rc=1
  kubectl -n "$ns" delete pod "$pod" --ignore-not-found --wait=false >/dev/null 2>&1
  [ "$rc" = 0 ] && echo allow || echo deny
}

API="shop-api.shop.svc.cluster.local"
PG="shop-postgres-rw.shop"
NODE_IP="$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')"
NODE_PORT="$(kubectl -n shop get svc shop-api -o jsonpath='{.spec.ports[0].nodePort}')"

echo "| Источник | Назначение | Порт/протокол | Ожидаемо | Фактически |"
echo "| --- | --- | --- | --- | --- |"
# shop: each source -> each other listening workload
echo "| api | postgres | 5432/TCP | allow | $(exec_in shop app.kubernetes.io/name=api nc -z -w 3 "$PG" 5432) |"
echo "| worker | api | 80/TCP | deny | $(exec_in shop app.kubernetes.io/name=worker nc -z -w 3 "$API" 80) |"
echo "| worker | postgres | 5432/TCP | allow | $(exec_in shop app.kubernetes.io/name=worker nc -z -w 3 "$PG" 5432) |"
echo "| postgres | api | 80/TCP | deny | $(exec_in shop app.kubernetes.io/name=postgres bash -c "timeout 3 bash -c '</dev/tcp/$API/80'") |"
# other namespaces
echo "| Prometheus | api | 8080/TCP | allow | $(run_once monitoring app.kubernetes.io/name=prometheus wget -q -O /dev/null -T 3 "http://$API/health") |"
echo "| client | api | 80/TCP | allow | $(run_once default app.kubernetes.io/name=client wget -q -O /dev/null -T 3 "http://$API/health") |"
echo "| client | postgres | 5432/TCP | deny | $(run_once default app.kubernetes.io/name=client nc -z -w 3 "$PG" 5432) |"
echo "| external | api via NodePort | $NODE_PORT/TCP | allow | $(curl -s -o /dev/null -m 3 "http://$NODE_IP:$NODE_PORT/health" && echo allow || echo deny) |"
