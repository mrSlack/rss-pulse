#!/usr/bin/env bash
# Restores the production database from a backup created by
# backup-production.sh (pg_dump custom format, *.dump).
#
#   scripts/restore-production.sh [--skip-safety-backup] --yes backups/production/pulse-<timestamp>.dump
#
# Steps: check the archive, back up the current database (unless
# --skip-safety-backup), stop the api and web services, drop and recreate the
# database, restore the archive, start the services and wait for their health
# checks. Every row written after the backup was taken is lost.

set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/compose.production.yml"
env_file="$repo_root/.env.production"

usage() {
  printf 'Usage: %s [--skip-safety-backup] --yes <backup-file.dump>\n' "$0" >&2
}

confirmed=false
safety_backup=true
backup_file=""
for argument in "$@"; do
  case "$argument" in
    --yes) confirmed=true ;;
    --skip-safety-backup) safety_backup=false ;;
    -h|--help) usage; exit 0 ;;
    -*) printf 'Unknown option: %s\n' "$argument" >&2; usage; exit 1 ;;
    *)
      if [[ -n "$backup_file" ]]; then
        usage
        exit 1
      fi
      backup_file="$argument"
      ;;
  esac
done

if [[ -z "$backup_file" ]]; then
  usage
  exit 1
fi
if [[ ! -f "$backup_file" || ! -r "$backup_file" ]]; then
  printf 'Backup file %s does not exist or is not readable.\n' "$backup_file" >&2
  exit 1
fi
if [[ "$backup_file" == *.sql ]]; then
  printf 'Plain SQL backups from older versions are not restored by this script; see the README for the manual procedure.\n' >&2
  exit 1
fi
if [[ "$confirmed" != true ]]; then
  printf 'This replaces the entire production database with %s.\n' "$backup_file" >&2
  printf 'All changes made after that backup are lost. Re-run with --yes to continue.\n' >&2
  exit 1
fi
if [[ ! -f "$env_file" ]]; then
  printf 'Missing %s. Copy .env.production.example and configure it first.\n' "$env_file" >&2
  exit 1
fi

wait_timeout="${DEPLOY_WAIT_TIMEOUT:-900}"
if ! [[ "$wait_timeout" =~ ^[1-9][0-9]*$ ]]; then
  printf 'DEPLOY_WAIT_TIMEOUT must be a positive number of seconds, got %s\n' "$wait_timeout" >&2
  exit 1
fi

compose=(docker compose --env-file "$env_file" -f "$compose_file")

printf 'Starting the database service\n'
"${compose[@]}" up -d --wait --wait-timeout "$wait_timeout" db

printf 'Checking the backup archive\n'
if ! "${compose[@]}" exec -T db pg_restore --list < "$backup_file" > /dev/null; then
  printf '%s is not a readable pg_dump archive; nothing was changed.\n' "$backup_file" >&2
  exit 1
fi

if [[ "$safety_backup" == true ]]; then
  printf 'Backing up the current database before restoring\n'
  safety_file="$("$repo_root/scripts/backup-production.sh")"
  printf 'Current database saved to %s\n' "$safety_file"
fi

printf 'Stopping the api and web services\n'
"${compose[@]}" stop api web

printf 'Recreating the database\n'
# shellcheck disable=SC2016 # expanded by the shell inside the db container
"${compose[@]}" exec -T db sh -c 'dropdb -U "$POSTGRES_USER" --force --if-exists "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'

printf 'Restoring %s\n' "$backup_file"
# shellcheck disable=SC2016 # expanded by the shell inside the db container
"${compose[@]}" exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --exit-on-error' < "$backup_file"

printf 'Starting the services and waiting for health checks\n'
"${compose[@]}" up -d --wait --wait-timeout "$wait_timeout"

printf 'Restore completed from %s\n' "$backup_file"
"${compose[@]}" ps
