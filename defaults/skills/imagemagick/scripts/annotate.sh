#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: annotate.sh <input> <output> <text> [options...]"
    echo ""
    echo "Add text to an image."
    echo ""
    echo "Options:"
    echo "  --gravity center|north|south|...  Text placement"
    echo "  --font <name>                     Font name"
    echo "  --size N                          Font size in points"
    echo "  --color <color>                   Text color (name or hex)"
    echo "  --background <color>              Background behind text"
    echo "  --offset +X+Y                     Offset from gravity"
    exit 0
fi

INPUT="$1"
OUTPUT="$2"
TEXT="$3"
shift 3

GRAVITY="south"
FONT=""
SIZE="24"
COLOR="white"
BG=""
OFFSET=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --gravity)    GRAVITY="$2"; shift 2 ;;
        --font)       FONT="$2"; shift 2 ;;
        --size)       SIZE="$2"; shift 2 ;;
        --color)      COLOR="$2"; shift 2 ;;
        --background) BG="$2"; shift 2 ;;
        --offset)     OFFSET="$2"; shift 2 ;;
        *)            shift ;;
    esac
done

ARGS=(-gravity "$GRAVITY" -pointsize "$SIZE" -fill "$COLOR")
[[ -n "$FONT" ]] && ARGS+=(-font "$FONT")
[[ -n "$BG" ]] && ARGS+=(-undercolor "$BG")
[[ -n "$OFFSET" ]] && ARGS+=(-geometry "$OFFSET")
ARGS+=(-annotate +0+10 "$TEXT")

echo "Annotating: $INPUT -> $OUTPUT"
magick "$INPUT" "${ARGS[@]}" "$OUTPUT"

if [[ -f "$OUTPUT" ]]; then
    SIZE=$(wc -c < "$OUTPUT" | tr -d ' ')
    echo "Done. Output: $OUTPUT ($SIZE bytes)"
else
    echo "Error: output file was not created" >&2
    exit 1
fi
