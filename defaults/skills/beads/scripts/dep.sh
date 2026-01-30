#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: dep.sh <action> <args...>"
    echo ""
    echo "Manage dependencies between issues."
    echo ""
    echo "Actions:"
    echo "  add <child> <parent>     child depends on parent"
    echo "  remove <child> <parent>  Remove dependency"
    echo "  list <id>                List dependencies"
    echo "  tree <id>                Show dependency tree"
    exit 0
fi

exec bd dep "$@"
