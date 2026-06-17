#!/usr/bin/env bash
# Deploy and (re)start the long-soak stress run on a dedicated host (default:
# misaki). Cross-compiles loadgen locally — the host needs only Docker, no Go —
# ships it with the compose files, builds the image, and brings the stack up.
#
#   ./deploy.sh                 # build + ship + up on $HOST
#   DURATION=72h WORKERS=64 ./deploy.sh
#   HOST=otherbox ./deploy.sh
#
# Tunables (also honored by docker-compose.yml at run time): DURATION, WORKERS,
# GOMAXPROCS. See README.md for checking state and stopping.
set -euo pipefail

HOST=${HOST:-misaki}
REMOTE_DIR=${REMOTE_DIR:-memcache-stress}

cd "$(dirname "$0")"

echo ">> cross-compiling loadgen (linux/amd64, static)"
( cd .. && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o stress/loadgen ./cmd/loadgen )

echo ">> shipping to $HOST:$REMOTE_DIR"
ssh "$HOST" "mkdir -p '$REMOTE_DIR/data'"
scp Dockerfile.loadgen docker-compose.yml loadgen "$HOST:$REMOTE_DIR/"

echo ">> building image and starting the stack on $HOST"
ssh "$HOST" "cd '$REMOTE_DIR' && \
  DURATION='${DURATION:-336h}' WORKERS='${WORKERS:-48}' GOMAXPROCS='${GOMAXPROCS:-8}' \
  docker compose up -d --build"

echo
echo ">> started. check state with:"
echo "   ssh $HOST 'cat $REMOTE_DIR/data/status.txt'"
echo "   ssh $HOST 'cd $REMOTE_DIR && docker compose logs --tail=20 loadgen'"
