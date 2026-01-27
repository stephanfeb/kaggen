#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: convert_img.sh <input> <output> [options...]"
    echo ""
    echo "Convert and transform images using ImageMagick."
    echo ""
    echo "Options:"
    echo "  --resize WxH      Resize to fit (preserves aspect ratio)"
    echo "  --resize WxH!     Force exact dimensions"
    echo "  --resize N%       Scale by percentage"
    echo "  --crop WxH+X+Y   Crop region"
    echo "  --quality N       Output quality (1-100)"
    echo "  --strip           Remove EXIF metadata"
    echo "  --rotate N        Rotate by degrees"
    echo "  --flip            Flip vertically"
    echo "  --flop            Flip horizontally"
    echo "  --grayscale       Convert to grayscale"
    echo "  --auto-orient     Fix orientation from EXIF"
    exit 0
fi

INPUT="$1"
OUTPUT="$2"
shift 2

if [[ ! -f "$INPUT" ]]; then
    echo "Error: input file not found: $INPUT" >&2
    exit 1
fi

ARGS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --resize)
            ARGS+=(-resize "$2")
            shift 2
            ;;
        --crop)
            ARGS+=(-crop "$2")
            shift 2
            ;;
        --quality)
            ARGS+=(-quality "$2")
            shift 2
            ;;
        --strip)
            ARGS+=(-strip)
            shift
            ;;
        --rotate)
            ARGS+=(-rotate "$2")
            shift 2
            ;;
        --flip)
            ARGS+=(-flip)
            shift
            ;;
        --flop)
            ARGS+=(-flop)
            shift
            ;;
        --grayscale)
            ARGS+=(-colorspace Gray)
            shift
            ;;
        --auto-orient)
            ARGS+=(-auto-orient)
            shift
            ;;
        *)
            ARGS+=("$1")
            shift
            ;;
    esac
done

echo "Converting: $INPUT -> $OUTPUT"
magick "$INPUT" "${ARGS[@]}" "$OUTPUT"

if [[ -f "$OUTPUT" ]]; then
    SIZE=$(wc -c < "$OUTPUT" | tr -d ' ')
    DIMS=$(magick identify -format '%wx%h' "$OUTPUT" 2>/dev/null || echo "unknown")
    echo "Done. Output: $OUTPUT ($SIZE bytes, ${DIMS})"
else
    echo "Error: output file was not created" >&2
    exit 1
fi
