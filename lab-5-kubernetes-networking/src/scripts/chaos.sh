#!/usr/bin/bash

exit=0

cleanup() {
  exit=1
}

trap cleanup SIGINT SIGTERM

while [ "$exit" -eq 0 ]; do

  POD=$(kubectl -n shop get po -l "app.kubernetes.io/name in (api,worker)" --field-selector=status.phase=Running -o name | shuf -n 1)

  if [ -n "$POD" ]; then
    kubectl -n shop delete "$POD" --wait=false
  fi

  sleep 6s &
  wait $! 2>/dev/null

done
