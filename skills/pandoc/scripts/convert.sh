#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: convert.sh <input> <output> [extra pandoc flags...]"
    echo ""
    echo "Convert a document between formats. Formats are detected from file extensions."
    echo ""
    echo "Examples:"
    echo "  convert.sh report.md report.pdf"
    echo "  convert.sh notes.md notes.docx"
    echo "  convert.sh page.html page.md"
    echo "  convert.sh doc.md doc.html --standalone"
    exit 0
fi

INPUT="$1"
OUTPUT="$2"
shift 2

if [[ ! -f "$INPUT" ]]; then
    echo "Error: input file not found: $INPUT" >&2
    exit 1
fi

# Build pandoc command
PANDOC_ARGS=(-o "$OUTPUT")

# Add smart defaults based on output format
case "$OUTPUT" in
    *.pdf)
        # Check for LaTeX availability
        if command -v pdflatex >/dev/null 2>&1; then
            PANDOC_ARGS+=(--pdf-engine=pdflatex)
        elif command -v xelatex >/dev/null 2>&1; then
            PANDOC_ARGS+=(--pdf-engine=xelatex)
        elif command -v wkhtmltopdf >/dev/null 2>&1; then
            PANDOC_ARGS+=(--pdf-engine=wkhtmltopdf)
        else
            echo "Warning: No PDF engine found. Install texlive or wkhtmltopdf." >&2
        fi
        ;;
    *.html)
        # Default to standalone HTML unless user overrides
        if [[ ! " $* " =~ " --standalone " ]] && [[ ! " $* " =~ " -s " ]]; then
            PANDOC_ARGS+=(--standalone)
        fi
        ;;
    *.docx)
        # Ensure reference doc is used if available
        ;;
esac

# Add any extra flags
PANDOC_ARGS+=("$@")

echo "Converting: $INPUT -> $OUTPUT"
pandoc "$INPUT" "${PANDOC_ARGS[@]}"

# Report result
if [[ -f "$OUTPUT" ]]; then
    SIZE=$(wc -c < "$OUTPUT" | tr -d ' ')
    echo "Done. Output: $OUTPUT ($SIZE bytes)"
else
    echo "Error: output file was not created" >&2
    exit 1
fi
