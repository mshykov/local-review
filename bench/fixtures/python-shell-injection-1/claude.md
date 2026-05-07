## Critical Issues

- `scripts/archive.py:10` — **Command injection via `shell=True`**.
  The diff swaps a safe argv list for an interpolated shell string
  built from a user-supplied `repo` argument. Any caller passing
  ``"foo; rm -rf ~"`` runs arbitrary commands.
  *Suggested fix:* keep the original argv form (``["tar", "-czf",
  ...]``) with ``shell=False``, or sanitise/validate ``repo`` against
  an allow-list before constructing the shell string.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

*(None)*
