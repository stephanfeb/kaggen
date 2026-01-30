---
name: imagemagick
description: Process and transform images using ImageMagick (resize, convert, composite, annotate, optimize)
---

# ImageMagick — Image Processing

Use this skill when the user asks to resize, crop, convert, annotate, composite, or otherwise transform images.

## Available Commands

Run operations via `skill_run` with the scripts below. All scripts accept `--help`.

### convert_img.sh — Format conversion and basic transforms

```bash
bash scripts/convert_img.sh <input> <output> [options...]
```

Options:
- `--resize WxH` — Resize to fit within dimensions (preserves aspect ratio)
- `--resize WxH!` — Force exact dimensions (may distort)
- `--resize 50%` — Scale by percentage
- `--crop WxH+X+Y` — Crop region
- `--quality N` — Output quality (1-100, for JPEG/WebP)
- `--strip` — Remove EXIF metadata
- `--rotate N` — Rotate by degrees
- `--flip` — Flip vertically
- `--flop` — Flip horizontally
- `--grayscale` — Convert to grayscale
- `--auto-orient` — Fix orientation from EXIF

Examples:

```bash
# Convert PNG to JPEG at 85% quality
bash scripts/convert_img.sh photo.png photo.jpg --quality 85

# Resize to max 800px wide, strip metadata
bash scripts/convert_img.sh large.jpg thumb.jpg --resize 800x --strip

# Crop a 500x500 region from coordinates 100,200
bash scripts/convert_img.sh photo.jpg cropped.jpg --crop 500x500+100+200

# Auto-orient and convert to WebP
bash scripts/convert_img.sh camera.jpg output.webp --auto-orient
```

### composite.sh — Overlay and combine images

```bash
bash scripts/composite.sh <background> <overlay> <output> [options...]
```

Options:
- `--gravity center|northwest|southeast|...` — Overlay placement
- `--opacity N` — Overlay opacity (0-100)
- `--offset +X+Y` — Pixel offset from gravity point

Examples:

```bash
# Add watermark at bottom-right, 30% opacity
bash scripts/composite.sh photo.jpg watermark.png output.jpg --gravity southeast --opacity 30

# Center a logo on a background
bash scripts/composite.sh bg.png logo.png result.png --gravity center
```

### annotate.sh — Add text to images

```bash
bash scripts/annotate.sh <input> <output> <text> [options...]
```

Options:
- `--gravity center|north|south|...` — Text placement
- `--font <name>` — Font name
- `--size N` — Font size in points
- `--color <color>` — Text color (name or hex)
- `--background <color>` — Background behind text
- `--offset +X+Y` — Offset from gravity

Examples:

```bash
# Add caption at bottom
bash scripts/annotate.sh photo.jpg captioned.jpg "Summer 2025" --gravity south --size 36 --color white

# Add title with background
bash scripts/annotate.sh img.png titled.png "Report Cover" --gravity center --size 48 --background "#00000080"
```

### optimize.sh — Optimize images for web

```bash
bash scripts/optimize.sh <input> [output] [--max-width N] [--max-height N] [--quality N]
```

If output is omitted, overwrites input. Strips metadata, resizes if over max dimensions, and compresses.

Examples:

```bash
# Optimize for web (max 1920px wide, quality 80)
bash scripts/optimize.sh photo.jpg web_photo.jpg --max-width 1920 --quality 80

# Batch optimize all JPEGs in a directory
for f in *.jpg; do bash scripts/optimize.sh "$f" "optimized/$f" --max-width 1200; done
```

### info.sh — Image metadata and dimensions

```bash
bash scripts/info.sh <file>
```

Returns: format, dimensions, color space, file size, bit depth, and EXIF data (if present).

## Format Support

| Format | Read | Write | Notes |
|--------|------|-------|-------|
| JPEG | Yes | Yes | Lossy, best for photos |
| PNG | Yes | Yes | Lossless, supports transparency |
| WebP | Yes | Yes | Modern web format, good compression |
| GIF | Yes | Yes | Animation supported |
| TIFF | Yes | Yes | High quality, large files |
| BMP | Yes | Yes | Uncompressed |
| SVG | Yes | Limited | Rasterization only |
| HEIC | Depends | No | Requires libheif |
| PDF | Yes | Yes | Single/multi page |
| ICO | Yes | Yes | Favicon format |

## Tips

- Always use `--auto-orient` when processing camera photos to fix rotation.
- Use `--strip` to remove EXIF data for privacy when sharing images.
- For batch operations, combine with shell loops or use `mogrify` (in-place variant).
- WebP typically gives 25-35% smaller files than JPEG at equivalent quality.
- Use `-define jpeg:extent=200KB` to target a specific file size.
