#!/usr/bin/env bash
# One-time server preparation. Run as root on the server:
#
#   sudo bash setup-server.sh racebot "ssh-ed25519 AAAA... race-bot-deploy"
#
# Creates the deploy user, lets its systemd user services run without an
# active login (linger) and authorizes the CI deploy key.
set -euo pipefail

user="${1:?usage: setup-server.sh <user> <public key>}"
pubkey="${2:?usage: setup-server.sh <user> <public key>}"

if ! id "$user" >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash "$user"
fi
loginctl enable-linger "$user"

home="$(getent passwd "$user" | cut -d: -f6)"
install -d -m 700 -o "$user" -g "$user" "$home/.ssh"
touch "$home/.ssh/authorized_keys"
grep -qxF "$pubkey" "$home/.ssh/authorized_keys" || echo "$pubkey" >> "$home/.ssh/authorized_keys"
chown "$user:$user" "$home/.ssh/authorized_keys"
chmod 600 "$home/.ssh/authorized_keys"

echo "ok: user $user is ready for deploys"
