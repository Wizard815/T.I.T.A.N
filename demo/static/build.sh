#!/usr/bin/env bash
# Builds one self-contained demo page: the real page with demo/static/mock.js
# in front of its script, so every /api call is answered in the browser and a
# one-minute story plays on a loop. Usage: demo/static/build.sh [out.html]
set -euo pipefail
shopt -u patsub_replacement 2>/dev/null || true # keep & in mock.js as it is
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out=${1:-$here/titan-demo.html}
page=$root/internal/web/page.html
banner='<div class="demo-bar" role="note">Demo: a made-up homelab on a one-minute loop. Sign in as <b>wizard</b> (owner), <b>sam</b> (family) or <b>alex</b> (friend), any password.</div>'
style='<style>.demo-bar{background:#2a2410;color:#f5e6b8;font:13px/1.4 system-ui,sans-serif;padding:6px 16px;text-align:center}.demo-bar b{color:#fff}</style>'
html=$(cat "$page")
html=${html/<title>T.I.T.A.N<\/title>/<title>T.I.T.A.N demo</title>$style}
html=${html/<body>/<body>$banner}
mock=$(cat "$here/mock.js")
html=${html/$'\n'<script>/$'\n'<script>$mock</script>$'\n'<script>}
printf '%s\n' "$html" > "$out"
echo "wrote $out"
