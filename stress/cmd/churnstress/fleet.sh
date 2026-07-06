#!/usr/bin/env bash
# Manage the local memcached fleet for churnstress on the misaki box.
#   ./fleet.sh up [N]     # start N containers cs00..cs(N-1), ports 11300+i
#   ./fleet.sh down [N]   # remove them
#   ./fleet.sh ps [N]     # show status
# Each container: memcached:1.6, 64 MB, published on 127.0.0.1:(11300+i).
set -euo pipefail

CMD=${1:-up}
N=${2:-48}
BASE=${BASE_PORT:-11300}
PREFIX=${PREFIX:-cs}
IMG=memcached:1.6

name() { printf '%s%02d' "$PREFIX" "$1"; }

case "$CMD" in
  up)
    echo ">> starting $N memcached (${PREFIX}00..$(name $((N-1))))"
    for i in $(seq 0 $((N-1))); do
      docker rm -f "$(name "$i")" >/dev/null 2>&1 || true
      docker run -d --name "$(name "$i")" \
        -p "127.0.0.1:$((BASE+i)):11211" \
        "$IMG" -m 64 -c 4096 -t 2 >/dev/null
    done
    echo ">> up: $(docker ps --filter "name=^${PREFIX}" --format '{{.Names}}' | wc -l | tr -d ' ') running"
    ;;
  down)
    echo ">> removing $N containers"
    for i in $(seq 0 $((N-1))); do docker rm -f "$(name "$i")" >/dev/null 2>&1 || true; done
    echo ">> done"
    ;;
  ps)
    docker ps -a --filter "name=^${PREFIX}" --format '{{.Names}} {{.Status}} {{.Ports}}' | sort | head -n "$N"
    ;;
  *) echo "usage: $0 up|down|ps [N]"; exit 1 ;;
esac
