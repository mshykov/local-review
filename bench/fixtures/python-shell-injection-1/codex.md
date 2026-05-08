## Major Issues

- scripts/archive.py:11 — using `shell=True` with f-string built from
  `repo` is shell-injection-prone. Prefer the argv form.

## Warnings

- scripts/archive.py:8 — building paths via f-string instead of
  pathlib loses the cross-platform handling that was there before.

## Info / Notes

*(None)*
