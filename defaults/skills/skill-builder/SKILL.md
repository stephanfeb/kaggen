---
name: skill-builder
description: Scaffold, validate, and install new Kaggen skills from descriptions or templates
---

# Skill Builder — Create New Kaggen Skills

Use this skill when the user asks to create, build, or add a new skill to Kaggen. This skill helps you scaffold a complete skill directory with proper conventions, validate it, and install it.

## Workflow

1. **Understand** what CLI tool or capability the skill wraps
2. **Scaffold** the directory structure with `scaffold.sh`
3. **Write** the SKILL.md and bash scripts (you do this directly with your file tools)
4. **Validate** the result with `validate.sh`
5. **Install** into `~/.kaggen/skills/` with `install.sh`
6. **Remind** the user to reload skills with `kill -HUP $(pgrep kaggen)`

## Available Commands

### scaffold.sh — Create skill skeleton

```bash
bash scripts/scaffold.sh <name> [output_dir]
```

Creates a directory with a template SKILL.md and empty scripts/ folder. Default output_dir is the current working directory.

### validate.sh — Lint a skill directory

```bash
bash scripts/validate.sh <skill_dir>
```

Checks:
- SKILL.md exists with valid `name:` and `description:` frontmatter
- All scripts/*.sh have a shebang, pass `bash -n` syntax check, and respond to `--help`

Exits 0 on pass, 1 on failure with diagnostics.

### install.sh — Install to ~/.kaggen/skills/

```bash
bash scripts/install.sh <skill_dir> [--force]
```

Copies the skill directory into `~/.kaggen/skills/`, sets executable permissions, and warns on name collisions (use `--force` to overwrite).

## Skill Conventions

When writing a new skill, follow these conventions exactly:

### SKILL.md Format

```yaml
---
name: <lowercase-hyphenated-name>
description: <one-line description of what the skill does>
---

# <Name> — <Short Title>

<When to use this skill — one paragraph.>

## Available Commands

<For each script, document:>

### <script_name>.sh — <what it does>

\`\`\`bash
bash scripts/<script_name>.sh <args> [options...]
\`\`\`

<Options list, then examples.>

## <Reference Table> (if applicable)

| Column | Description |
|--------|-------------|
| ...    | ...         |

## Tips

- <Practical tips and gotchas>
```

### Script Conventions

Every bash script MUST:

1. Start with `#!/usr/bin/env bash` and `set -euo pipefail`
2. Support `--help` flag (print usage and exit 0)
3. Validate inputs and print errors to stderr (`>&2`)
4. Report results to stdout (filename, size, counts)
5. Exit 0 on success, 1 on failure

Template:

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: script.sh <required_arg> [options...]"
    echo ""
    echo "Description of what this script does."
    exit 0
fi

# Validate inputs
INPUT="$1"
if [[ ! -f "$INPUT" ]]; then
    echo "Error: file not found: $INPUT" >&2
    exit 1
fi

# Do work...
echo "Done."
```

### Naming

- Skill name: lowercase, hyphens for word separation (e.g. `ffmpeg-video`, `aws-s3`)
- Script names: lowercase, underscores, descriptive (e.g. `convert.sh`, `batch_process.sh`)
- Keep script count to 3-6 per skill — each script should do one thing well

### What Makes a Good Skill

- Wraps a CLI tool the user has installed (pandoc, ffmpeg, jq, curl, etc.)
- Provides 3-5 scripts covering the tool's most common operations
- Includes practical examples for every command
- Has a reference table for formats, options, or capabilities
- Includes tips section with gotchas and best practices
