# determinize-peribolos

Normalizes and formats Peribolos configuration files for consistency.

## Overview

Determinize-peribolos ensures that Peribolos (GitHub organization management) configuration files are consistently formatted and ordered.

## Usage

```bash
determinize-peribolos \
  --config-path=/path/to/peribolos.yaml \
  --confirm
```

## Options

- `--config-path`: Path to Peribolos configuration file
- `--confirm`: Actually write changes (required for modifications)

## How It Works

1. Loads Peribolos configuration
2. Normalizes YAML structure and ordering
3. Sorts fields consistently
4. Writes normalized config back (if --confirm is used)

## Related

- Peribolos - GitHub organization management tool

