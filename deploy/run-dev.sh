#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PWD_FILE="${SCRIPT_DIR}/.pwd"

if [ ! -f "$PWD_FILE" ]; then
  echo "Missing ${PWD_FILE}"
  echo "Create it with: echo 'your-app-password' > ${PWD_FILE} && chmod 600 ${PWD_FILE}"
  exit 1
fi

GMAIL_APP_PASSWORD="$(cat "$PWD_FILE" | tr -d '[:space:]')"
export GMAIL_APP_PASSWORD
export API_HEALTH_URL="http://192.168.1.28:8088/api/health"

exec easeprobe -f "${SCRIPT_DIR}/../config.yaml"
