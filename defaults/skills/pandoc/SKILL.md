---
name: pandoc
description: Convert documents between formats using Pandoc (Markdown, HTML, PDF, DOCX, LaTeX, EPUB, and more)
---

# Pandoc — Document Conversion

Use this skill when the user asks to convert a document between formats, generate a PDF from Markdown, export notes to DOCX, or any format transformation task.

## Available Commands

Run conversions via `skill_run` with the scripts below. All scripts accept `--help`.

### convert.sh — General conversion

```bash
bash scripts/convert.sh <input> <output> [extra pandoc flags...]
```

The script auto-detects formats from file extensions. Examples:

```bash
# Markdown to PDF (via LaTeX)
bash scripts/convert.sh report.md report.pdf

# Markdown to DOCX
bash scripts/convert.sh notes.md notes.docx

# HTML to Markdown
bash scripts/convert.sh page.html page.md

# Markdown to HTML (standalone with CSS)
bash scripts/convert.sh doc.md doc.html --standalone --css=style.css

# LaTeX to PDF
bash scripts/convert.sh paper.tex paper.pdf

# Markdown to EPUB
bash scripts/convert.sh book.md book.epub --metadata title="My Book"

# DOCX to Markdown
bash scripts/convert.sh document.docx document.md --wrap=none
```

### batch_convert.sh — Convert multiple files

```bash
bash scripts/batch_convert.sh <input_dir> <output_dir> <from_ext> <to_ext> [extra flags...]
```

Examples:

```bash
# Convert all .md files to .html
bash scripts/batch_convert.sh ./docs ./output md html --standalone

# Convert all .docx to .md
bash scripts/batch_convert.sh ./word ./markdown docx md --wrap=none
```

### inspect.sh — Show document metadata and structure

```bash
bash scripts/inspect.sh <file>
```

Returns: format, word count, metadata (title, author, date), and section headings.

## Format Reference

| Format | Extensions | Read | Write | Notes |
|--------|-----------|------|-------|-------|
| Markdown | .md | Yes | Yes | CommonMark and GFM variants |
| HTML | .html | Yes | Yes | Use `--standalone` for full page |
| PDF | .pdf | No | Yes | Requires LaTeX (`pdflatex`) or `wkhtmltopdf` |
| DOCX | .docx | Yes | Yes | Preserves basic formatting |
| LaTeX | .tex | Yes | Yes | Full LaTeX support |
| EPUB | .epub | Yes | Yes | E-book format |
| reStructuredText | .rst | Yes | Yes | Sphinx-compatible |
| Org Mode | .org | Yes | Yes | Emacs org format |
| Plain text | .txt | Yes | Yes | Stripped formatting |
| PPTX | .pptx | No | Yes | Slide decks from headings |
| CSV | .csv | Yes | No | Tables only |
| JSON (AST) | .json | Yes | Yes | Pandoc native AST |

## Tips

- For PDF output, LaTeX must be installed. If missing, suggest `--pdf-engine=wkhtmltopdf` as fallback.
- Use `--toc` to add a table of contents to longer documents.
- Use `--number-sections` for numbered headings.
- Use `--reference-doc=template.docx` to apply a Word template.
- Use `--template=custom.tex` for LaTeX customization.
- When converting from DOCX, add `--wrap=none` to avoid unwanted line breaks.
- For Markdown tables in PDF, `--columns=80` helps prevent overflow.
