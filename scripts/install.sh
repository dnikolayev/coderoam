#!/bin/sh
set -eu

agent="${CODEROAM_AGENT:-auto}"
session_id="${CODEROAM_SESSION_ID:-codex-session}"
workdir="${CODEROAM_WORKDIR:-$(pwd)}"
install_mode="${CODEROAM_INSTALL_MODE:-stable}"

usage() {
  cat <<'EOF'
Usage: install.sh [--agent auto|codex|claude|gemini|opencode|none] [--session-id ID] [--workdir PATH] [--stable|--head]

Environment:
  CODEROAM_AGENT         Agent setup target. Default: auto
  CODEROAM_SESSION_ID    Active session id. Default: codex-session
  CODEROAM_WORKDIR       Workspace path for generated agent commands. Default: current directory
  CODEROAM_INSTALL_MODE  "stable" or "head". Default: stable
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --agent)
      agent="${2:-}"
      shift 2
      ;;
    --session-id)
      session_id="${2:-}"
      shift 2
      ;;
    --workdir)
      workdir="${2:-}"
      shift 2
      ;;
    --stable)
      install_mode="stable"
      shift
      ;;
    --head)
      install_mode="head"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is required. Install it first: https://brew.sh/" >&2
  exit 1
fi

brew tap dnikolayev/coderoam https://github.com/dnikolayev/coderoam.git

if command -v brew >/dev/null 2>&1 && brew trust --help >/dev/null 2>&1; then
  brew trust dnikolayev/coderoam >/dev/null 2>&1 || true
fi

case "$install_mode" in
  stable)
    brew install dnikolayev/coderoam/coderoam
    ;;
  head)
    brew install --HEAD dnikolayev/coderoam/coderoam
    ;;
  *)
    echo "CODEROAM_INSTALL_MODE must be 'head' or 'stable'" >&2
    exit 2
    ;;
esac

coderoam setup --agent "$agent" --workdir "$workdir" --session-id "$session_id"
