#!/usr/bin/env bash
# Creates a compressed PostgreSQL backup (pg_dump custom format) of the
# production database in backups/production/ and deletes backups older than
# BACKUP_KEEP_DAYS days (default 14). The newest backup is never deleted.
# Progress goes to stderr; the path of the new backup is printed on stdout.
# Suitable for cron or a systemd timer (see the README).

set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/compose.production.yml"
env_file="$repo_root/.env.production"
backup_dir="$repo_root/backups/production"

keep_days="${BACKUP_KEEP_DAYS:-14}"
if ! [[ "$keep_days" =~ ^[1-9][0-9]*$ ]]; then
  printf 'BACKUP_KEEP_DAYS must be a positive number of days, got %s\n' "$keep_days" >&2
  exit 1
fi

if [[ ! -f "$env_file" ]]; then
  printf 'Missing %s. Copy .env.production.example and configure it first.\n' "$env_file" >&2
  exit 1
fi

compose=(docker compose --env-file "$env_file" -f "$compose_file")

umask 077
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"

backup_file="$backup_dir/pulse-$(date -u +%Y%m%dT%H%M%SZ).dump"
if [[ -e "$backup_file" ]]; then
  printf 'Backup %s already exists; try again in a second.\n' "$backup_file" >&2
  exit 1
fi
backup_tmp="$backup_dir/.pulse-backup.tmp.$$"
trap 'rm -f "$backup_tmp"' EXIT

printf 'Backing up the production database to %s\n' "$backup_file" >&2
# shellcheck disable=SC2016 # expanded by the shell inside the db container
"${compose[@]}" exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom' > "$backup_tmp"
if [[ ! -s "$backup_tmp" ]]; then
  printf 'Database backup is empty; keeping the previous backups.\n' >&2
  exit 1
fi
# Reading the archive's table of contents catches truncated or corrupt output.
if ! "${compose[@]}" exec -T db pg_restore --list < "$backup_tmp" > /dev/null; then
  printf 'Database backup is not a readable pg_dump archive; keeping the previous backups.\n' >&2
  exit 1
fi
chmod 600 "$backup_tmp"
mv "$backup_tmp" "$backup_file"

# Rotation: backup names sort chronologically, so the last name is the newest.
# Plain-SQL backups from older versions of update-production.sh rotate too.
shopt -s nullglob
backups=("$backup_dir"/pulse-*.dump "$backup_dir"/pulse-*.sql)
shopt -u nullglob
if (( ${#backups[@]} > 1 )); then
  mapfile -t sorted < <(printf '%s\n' "${backups[@]}" | sort)
  newest="${sorted[${#sorted[@]}-1]}"
  while IFS= read -r -d '' expired; do
    if [[ "$expired" != "$newest" ]]; then
      printf 'Deleting backup older than %s days: %s\n' "$keep_days" "$expired" >&2
      rm -f -- "$expired"
    fi
  done < <(find "$backup_dir" -maxdepth 1 -type f \( -name 'pulse-*.dump' -o -name 'pulse-*.sql' \) -mmin "+$((keep_days * 1440))" -print0)
fi

printf '%s\n' "$backup_file"
