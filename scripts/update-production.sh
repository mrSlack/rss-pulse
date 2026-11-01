#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/compose.production.yml"
env_file="$repo_root/.env.production"

wait_timeout="${DEPLOY_WAIT_TIMEOUT:-900}"
if ! [[ "$wait_timeout" =~ ^[1-9][0-9]*$ ]]; then
  printf 'DEPLOY_WAIT_TIMEOUT must be a positive number of seconds, got %s\n' "$wait_timeout" >&2
  exit 1
fi

if [[ ! -f "$env_file" ]]; then
  printf 'Missing %s. Copy .env.production.example and configure it first.\n' "$env_file" >&2
  exit 1
fi

if ! git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'This directory is not a Git checkout: %s\n' "$repo_root" >&2
  exit 1
fi

branch="$(git -C "$repo_root" branch --show-current)"
if [[ "$branch" != "master" ]]; then
  printf 'Expected branch master, found %s. Check out master before updating.\n' "${branch:-detached HEAD}" >&2
  exit 1
fi

if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
  printf 'Working tree is not clean. Commit or move local changes before updating production.\n' >&2
  exit 1
fi

compose=(docker compose --env-file "$env_file" -f "$compose_file")

# backup-production.sh prints the new backup's path; any failure stops the update.
if ! backup_file="$("$repo_root/scripts/backup-production.sh")"; then
  printf 'Database backup failed; stopping without updating production.\n' >&2
  exit 1
fi

printf 'Pulling origin/master with fast-forward only\n'
git -C "$repo_root" pull --ff-only origin master

printf 'Validating production Compose configuration\n'
"${compose[@]}" config -q

printf 'Pulling the PostgreSQL image\n'
"${compose[@]}" pull db

printf 'Building production images\n'
"${compose[@]}" build --pull

printf 'Deploying and waiting for health checks\n'
"${compose[@]}" up -d --wait --wait-timeout "$wait_timeout"

printf 'Production update completed. Database backup: %s\n' "$backup_file"
"${compose[@]}" ps
