#!/usr/bin/env bash
# T.I.T.A.N on a fake copy of Wizard's Unraid, with real models from
# OpenRouter through demo/orproxy (stops at ORPROXY_BUDGET dollars).
# Sign in at http://127.0.0.1:9910 as wizard, sam (family) or alex (friend);
# passwords are password-<name>.
set -euo pipefail
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$tmp"' EXIT
go build -o "$tmp/" ./cmd/titan ./cmd/titan-hands ./demo/fakeworld ./demo/orproxy
"$tmp/fakeworld" -addr 127.0.0.1:9900 -sock "$tmp/docker.sock" -containers demo/unraid/containers.json -tunnel Unraid-Cloudflared-Tunnel &
ORPROXY_BUDGET=${ORPROXY_BUDGET:-0.90} \
  ORPROXY_MODELS=${ORPROXY_MODELS:-"titan-guard=google/gemini-2.5-flash-lite titan-agent=google/gemini-2.5-flash titan-fallback=openai/gpt-4.1-nano"} \
  "$tmp/orproxy" &
sleep 0.5
# two MI50s for the server panel (the demo reads this machine's real CPU and memory)
for c in 0 1; do
  d="$tmp/sys/class/drm/card$c/device"; mkdir -p "$d/hwmon/hwmon$c"
  echo 34342961152 > "$d/mem_info_vram_total"; echo $(( (c + 1) * 9000000000 )) > "$d/mem_info_vram_used"
  echo $(( 20 + c * 47 )) > "$d/gpu_busy_percent"; echo $(( 48000 + c * 21000 )) > "$d/hwmon/hwmon$c/temp1_input"
done
TITAN_HANDS_SYS="$tmp/sys" TITAN_HANDS_DISKS="array=/ cache=$tmp" \
TITAN_HANDS_TOKEN=demo-hands-token-0123 TITAN_HANDS_ADDR=127.0.0.1:9911 DOCKER_SOCK="$tmp/docker.sock" \
  TITAN_HANDS_CONTAINERS="faith:restart:logs binhex-plexpass:restart Unraid-Cloudflared-Tunnel:restart argos" \
  TITAN_HANDS_AMP_URL=http://127.0.0.1:9901 TITAN_HANDS_AMP_USER=titan TITAN_HANDS_AMP_PASSWORD=demo-amp TITAN_HANDS_AMP_INSTANCES=Enshrouded01 \
  TITAN_HANDS_SYNCS="dnd=http://127.0.0.1:9900/dnd/sync" "$tmp/titan-hands" &
export TITAN_CONFIG=demo/unraid/titan.yaml TITAN_DATA="$tmp/titan.db" TITAN_HANDS_TOKEN=demo-hands-token-0123
for u in "wizard owner" "sam family" "alex friend"; do
  set -- $u; TITAN_PASSWORD="password-$1" "$tmp/titan" user add "$1" "$2"
done
TITAN_ADDR=127.0.0.1:9910 TITAN_EXPORT_TOKEN=demo-export "$tmp/titan" &
echo "T.I.T.A.N (Unraid copy, real models) on http://127.0.0.1:9910; spend at http://127.0.0.1:9920/spend"
wait
