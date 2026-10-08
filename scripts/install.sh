#!/bin/bash
# Install margin-todos on the tablet: the detector service, the panel QML, and the xovi diff that
# loads the panel into the writing screen. Needs xovi + qt-resource-rebuilder with a hashtab.
#   scripts/install.sh [ssh-host]
set -e
HOST="${1:-remarkable}"
DIR="$(cd "$(dirname "$0")/.." && pwd)"
ssh "$HOST" 'test -f /home/root/xovi/exthome/qt-resource-rebuilder/hashtab' \
  || { echo "xovi with a hashtab is required (xovi/rebuild_hashtable)"; exit 1; }
ssh "$HOST" 'mkdir -p /home/root/todo/img && systemctl stop margin-todos 2>/dev/null || true'
scp -q "$DIR/detector/margin-todos-detector" "$DIR/tablet/TodoPanel.qml" "$HOST":/home/root/todo/
scp -q "$DIR/tablet/margin-todos.service" "$HOST":/etc/systemd/system/
scp -q "$DIR/tablet/margin-todos.qmd" "$HOST":/home/root/xovi/exthome/qt-resource-rebuilder/
ssh "$HOST" 'systemctl daemon-reload && systemctl enable --now margin-todos && systemctl restart xochitl
  sleep 3; journalctl -u margin-todos -n 2 --no-pager'
