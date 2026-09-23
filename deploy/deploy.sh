#!/usr/bin/env bash
# Builds the bot for Linux and deploys it to a server over SSH as a systemd user service.
# Used by .github/workflows/deploy.yml, but works from a laptop too:
#
#   DEPLOY_HOST=1.2.3.4 DEPLOY_USER=racebot TELEGRAM_TOKEN=... ./deploy/deploy.sh
#
# Layout on the server (in the deploy user's home):
#   ~/race-bot/race-bot       binary (previous one kept as race-bot.prev)
#   ~/race-bot/env            bot settings, mode 600
#   ~/race-bot/data/          SQLite DB and tile cache — never touched by deploys
set -euo pipefail

: "${DEPLOY_HOST:?DEPLOY_HOST is required}"
: "${DEPLOY_USER:?DEPLOY_USER is required}"
: "${TELEGRAM_TOKEN:?TELEGRAM_TOKEN is required}"
DEPLOY_PORT="${DEPLOY_PORT:-22}"
DEPLOY_ARCH="${DEPLOY_ARCH:-amd64}"

ssh_opts=(-o BatchMode=yes)
if [[ -n "${DEPLOY_SSH_KEY_FILE:-}" ]]; then
  ssh_opts+=(-i "$DEPLOY_SSH_KEY_FILE" -o IdentitiesOnly=yes)
fi
if [[ -n "${DEPLOY_KNOWN_HOSTS_FILE:-}" ]]; then
  ssh_opts+=(-o UserKnownHostsFile="$DEPLOY_KNOWN_HOSTS_FILE" -o StrictHostKeyChecking=yes)
fi
remote="$DEPLOY_USER@$DEPLOY_HOST"

cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> build linux/$DEPLOY_ARCH"
CGO_ENABLED=0 GOOS=linux GOARCH="$DEPLOY_ARCH" \
  go build -trimpath -ldflags "-s -w" -o "$tmp/race-bot.new" ./cmd/bot

echo "==> env file"
# systemd EnvironmentFile; only variables that are set are written, the bot has defaults for the rest.
(
  umask 077
  for name in TELEGRAM_TOKEN TILE_USER_AGENT TILE_URL TZ IDLE_TIMEOUT MAX_ACCURACY_M MAX_SPEED_KMH; do
    value="${!name:-}"
    [[ -z "$value" ]] && continue
    if [[ "$value" == *[\"\\$'\n']* ]]; then
      echo "$name must not contain quotes, backslashes or newlines" >&2
      exit 1
    fi
    printf '%s="%s"\n' "$name" "$value"
  done > "$tmp/env.new"
)

echo "==> upload to $remote:$DEPLOY_PORT"
ssh "${ssh_opts[@]}" -p "$DEPLOY_PORT" "$remote" 'mkdir -p ~/race-bot/data'
scp "${ssh_opts[@]}" -P "$DEPLOY_PORT" -q \
  "$tmp/race-bot.new" "$tmp/env.new" deploy/race-bot.service "$remote:race-bot/"

echo "==> install and restart"
ssh "${ssh_opts[@]}" -p "$DEPLOY_PORT" "$remote" bash -s <<'REMOTE'
set -euo pipefail
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
cd ~/race-bot
chmod 600 env.new && mv env.new env
chmod 755 race-bot.new
[[ -f race-bot ]] && cp -p race-bot race-bot.prev
mv race-bot.new race-bot
mkdir -p ~/.config/systemd/user
mv race-bot.service ~/.config/systemd/user/race-bot.service
systemctl --user daemon-reload
systemctl --user enable --quiet race-bot
systemctl --user restart race-bot
sleep 5
if ! systemctl --user is-active --quiet race-bot; then
  echo "race-bot failed to start:" >&2
  journalctl --user -u race-bot -n 50 --no-pager >&2 || true
  exit 1
fi
systemctl --user --no-pager status race-bot | head -n 5
REMOTE

echo "==> deployed"
