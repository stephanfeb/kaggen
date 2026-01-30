#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: composite.sh <background> <overlay> <output> [options...]"
    echo ""
    echo "Overlay one image on another."
    echo ""
    echo "Options:"
    echo "  --gravity center|northwest|southeast|...  Overlay placement"
    echo "  --opacity N                               Overlay opacity (0-100)"
    echo "  --offset +X+Y                             Pixel offset from gravity"
    exit 0
fi

BG="$1"
OVERLAY="$2"
OUTPUT="$3"
shift 3

GRAVITY="center"
OPACITY=""
OFFSET=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --gravity)  GRAVITY="$2"; shift 2 ;;
        --opacity)  OPACITY="$2"; shift 2 ;;
        --offset)   OFFSET="$2"; shift 2 ;;
        *)          shift ;;
    esac
done

ARGS=()
if [[ -n "$OPACITY" ]]; then
    ARGS+=(\( "$OVERLAY" -alpha set -channel A -evaluate set "${OPACITY}%" +channel \))
else
    ARGS+=("$OVERLAY")
fi
ARGS+=(-gravity "$GRAVITY")
[[ -n "$OFFSET" ]] && ARGS+=(-geometry "$OFFSET")
ARGS+=(-composite "$OUTPUT")

echo "Compositing: $OVERLAY onto $BG -> $OUTPUT"
magick "$BG" "${ARGS[@]}"

if [[ -f "$OUTPUT" ]]; then
    SIZE=$(wc -c < "$OUTPUT" | tr -d ' ')
    echo "Done. Output: $OUTPUT ($SIZE bytes)"
else
    echo "Error: output file was not created" >&2
    exit 1
fi
