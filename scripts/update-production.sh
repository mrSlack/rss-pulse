#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/compose.production.yml"
env_file="$repo_root/.env.production"
backup_dir="$repo_root/backups/production"

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

mkdir -p "$backup_dir"
backup_file="$backup_dir/pulse-$(date -u +%Y%m%dT%H%M%SZ).sql"
backup_tmp="$backup_file.tmp.$$"
trap 'rm -f "$backup_tmp"' EXIT

compose=(docker compose --env-file "$env_file" -f "$compose_file")

printf 'Backing up the production database to %s\n' "$backup_file"
"${compose[@]}" exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' > "$backup_tmp"
if [[ ! -s "$backup_tmp" ]]; then
  printf 'Database backup is empty; stopping without updating production.\n' >&2
  exit 1
fi
chmod 600 "$backup_tmp"
mv "$backup_tmp" "$backup_file"

printf 'Pulling origin/master with fast-forward only\n'
git -C "$repo_root" pull --ff-only origin master

printf 'Validating production Compose configuration\n'
"${compose[@]}" config -q

printf 'Pulling the PostgreSQL image\n'
"${compose[@]}" pull db

printf 'Building production images\n'
"${compose[@]}" build --pull

printf 'Deploying and waiting for health checks\n'
"${compose[@]}" up -d --wait --wait-timeout 180

printf 'Production update completed. Database backup: %s\n' "$backup_file"
"${compose[@]}" ps
