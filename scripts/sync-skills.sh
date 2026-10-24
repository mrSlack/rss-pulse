#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HANDOFF_SKILL="$PROJECT_ROOT/.agents/skills/handoff/SKILL.md"
MODE="${1:-update}"

cd "$PROJECT_ROOT"

case "$MODE" in
  install)
    npx --yes --package=node@24 --package=skills@latest -- skills experimental_install --copy --yes
    ;;
  update)
    npx --yes --package=node@24 --package=skills@latest -- skills update --project --yes
    ;;
  *)
    printf 'Usage: %s [install|update]\n' "$0" >&2
    exit 2
    ;;
esac

if ! grep -Fq '## Project handoff persistence' "$HANDOFF_SKILL"; then
  cat >> "$HANDOFF_SKILL" <<'HANDOFF_RULES'

## Project handoff persistence

This skill is customized for the Pulse RSS reader project. When invoked in this repository, update the root `SESSION_HANDOFF.md` in place with a single aggregated, concise handoff for the current project state. Merge useful prior context with new work; remove stale or duplicate notes. Include the current date (`YYYY-MM-DD`), what works, what is incomplete or failing, and the next practical step. Do not write the project handoff to a temporary directory. Keep this customization in the project copy only; never change a global skill installation.
HANDOFF_RULES
fi
