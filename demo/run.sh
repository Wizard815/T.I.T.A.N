#!/usr/bin/env bash
# Runs T.I.T.A.N against fakeworld: nothing real is touched.
# Open http://127.0.0.1:9910 and sign in as wizard, sam (family) or alex
# (friend); every password is password-<name>.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$tmp"' EXIT
go build -o "$tmp/" ./cmd/titan ./cmd/titan-hands ./demo/fakeworld
"$tmp/fakeworld" -addr 127.0.0.1:9900 -sock "$tmp/docker.sock" &
sleep 0.5
TITAN_HANDS_TOKEN=demo-hands-token-0123 TITAN_HANDS_ADDR=127.0.0.1:9911 DOCKER_SOCK="$tmp/docker.sock" \
  TITAN_HANDS_CONTAINERS="minecraft valheim:restart faith:restart:logs hermes-sam:restart" \
  TITAN_HANDS_SYNCS="dnd=http://127.0.0.1:9900/dnd/sync" "$tmp/titan-hands" &
export TITAN_CONFIG=demo/titan.yaml TITAN_DATA="$tmp/titan.db" TITAN_HANDS_TOKEN=demo-hands-token-0123
for u in "wizard owner" "sam family" "alex friend"; do
  set -- $u; TITAN_PASSWORD="password-$1" "$tmp/titan" user add "$1" "$2"
done
TITAN_ADDR=127.0.0.1:9910 TITAN_EXPORT_TOKEN=demo-export "$tmp/titan" &
echo "T.I.T.A.N demo on http://127.0.0.1:9910 (Ctrl-C stops it)"
wait
