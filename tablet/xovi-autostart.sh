#!/bin/sh
# Starts xovi after boot, with a crash-loop guard. Installed as /home/root/xovi/autostart and run
# by xovi-autostart.service.
#
# xovi is tethered by design (a reboot comes back stock). Starting it at boot is safe on this
# tablet (unencrypted /home), but if a mod ever crashed reMarkable's app, its crash handler would
# reboot, this would start xovi again, and so on. So each boot counts an attempt, two minutes of
# the app staying up resets the count, and after two attempts in a row that didn't, the tablet
# stays stock until /home/root/xovi/autostart-attempts is deleted.
COUNT=/home/root/xovi/autostart-attempts
HASHTAB=/home/root/xovi/exthome/qt-resource-rebuilder/hashtab

n=$(cat "$COUNT" 2>/dev/null || echo 0)
if [ "$n" -ge 2 ]; then
    echo "xovi autostart: skipped; the app didn't stay up after the last $n starts. Delete $COUNT to re-enable."
    exit 0
fi
if [ ! -f "$HASHTAB" ]; then
    echo "xovi autostart: skipped; no hashtab (run xovi/rebuild_hashtable)."
    exit 0
fi

echo $((n + 1)) > "$COUNT"
sync
echo "xovi autostart: starting xovi (attempt $((n + 1)))"
/home/root/xovi/start

sleep 120
if systemctl is-active -q xochitl; then
    echo 0 > "$COUNT"
    echo "xovi autostart: the app stayed up for 2 minutes; attempts reset"
fi
