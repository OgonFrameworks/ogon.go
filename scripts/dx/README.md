# DX measurement scripts (DX-002..004, DX-008..009, DX-014)

| Script                    | Measures / verifies                          | Spec    |
|---------------------------|-----------------------------------------------|---------|
| ttfr.sh                   | Time-to-first-run (target <= 5 min).          | DX-002  |
| file-line-counters.sh     | Dev-written files/lines per golden path.     | DX-003  |
| cli-timing.sh             | p50/p95/p99 of non-generating CLI commands.  | DX-004  |
| explain-coverage.sh       | Every framework behavior has an explain topic.| DX-009  |
| error-remedy-check.sh     | Every diagnostic has a remedy.                | DX-008/014 |

Run them all:

```bash
for script in scripts/dx/*.sh; do
    bash "$script"
done
```

The scripts are POSIX-sh compatible (no bashisms beyond `set -euo
pipefail`) and write TSV to stdout for easy CI parsing.

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
