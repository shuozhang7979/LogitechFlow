#!/bin/sh
set -eu
cd "$(dirname "$0")"
DMG=$(ls Oink-*.dmg | head -n 1)
MOUNT=$(mktemp -d /tmp/oink-mnt.XXXXXX)
trap 'hdiutil detach "$MOUNT" -quiet || true; rmdir "$MOUNT" || true' EXIT
hdiutil attach "$DMG" -nobrowse -readonly -mountpoint "$MOUNT"
for proc in oink oink-backend oink-agent oink-file-provider OinkFileProvider OinkFinderSync OinkPaste; do
  pkill -x "$proc" 2>/dev/null || true
done
for _ in 1 2 3 4 5 6 7 8 9 10; do
  pgrep -x oink-backend >/dev/null 2>&1 || break
  sleep 0.3
done
rm -rf /Applications/Oink.app
ditto "$MOUNT/Oink.app" /Applications/Oink.app
xattr -cr /Applications/Oink.app
open /Applications/Oink.app
echo "Installed and launched /Applications/Oink.app!"
