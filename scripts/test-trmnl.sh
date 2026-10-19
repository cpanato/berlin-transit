#!/usr/bin/env bash
# Explicit local smoke test: collect S-Bahn departures and send them to TRMNL.
# Disable tracing before reading or expanding the secret webhook URL.
set +x
set -euo pipefail

usage() {
  printf 'Usage: %s [STATION_IDS]\n' "$0"
  printf 'Default: S Buch (Berlin), S2 — 900135001. Optional IDs are comma-separated.\n'
  printf 'Builds the binary, enables VBB collection and TRMNL sending, and stays on loopback.\n'
}

if [[ ${1:-} == --help || ${1:-} == -h ]]; then
  usage
  exit 0
fi
if (( $# > 1 )); then
  usage >&2
  exit 2
fi

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

if ! command -v go >/dev/null 2>&1; then
  printf 'Go is required; install the patched version specified in README.md.\n' >&2
  exit 1
fi
make build

if [[ -z ${TRMNL_WEBHOOK_URL:-} ]]; then
  if [[ ! -t 0 ]]; then
    printf 'Run interactively to enter the webhook URL, or set TRMNL_WEBHOOK_URL locally.\n' >&2
    exit 1
  fi
  if ! IFS= read -r -s -p 'TRMNL Webhook URL (hidden): ' TRMNL_WEBHOOK_URL; then
    printf '\nWebhook URL was not read.\n' >&2
    exit 1
  fi
  printf '\n'
fi
if [[ -z $TRMNL_WEBHOOK_URL ]]; then
  printf 'Webhook URL is required.\n' >&2
  exit 1
fi
export TRMNL_WEBHOOK_URL

export BERLIN_TRANSIT_COLLECTOR_ENABLED=true
export BERLIN_TRANSIT_COLLECTOR_STATIONS="${1-${BERLIN_TRANSIT_COLLECTOR_STATIONS:-900135001}}"
export BERLIN_TRANSIT_COLLECTOR_MODE=suburban
export BERLIN_TRANSIT_COLLECTOR_INTERVAL="${BERLIN_TRANSIT_COLLECTOR_INTERVAL:-30s}"
export BERLIN_TRANSIT_COLLECTOR_LIMIT="${BERLIN_TRANSIT_COLLECTOR_LIMIT:-8}"
export TRMNL_ENABLED=true
export TRMNL_INTERVAL="${TRMNL_INTERVAL:-5m}"
export TRMNL_MAX_DEPARTURES="${TRMNL_MAX_DEPARTURES:-8}"

printf 'Starting S-Bahn collection and TRMNL delivery; Ctrl+C stops the test.\n'
printf 'Allow up to one publishing interval (default 5m) for trmnl_sent, then refresh the plugin preview.\n'
exec "$repo_root/dist/berlin-transit" serve
