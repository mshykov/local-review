## Warnings

- app/config.py:7 — function annotation says `-> dict` but returns
  whatever yaml.load gives back; consider `dict[str, Any]`.
- app/config.py:1 — Path import is fine but `from pathlib import Path`
  is conventionally above the third-party imports.

## Info / Notes

- app/config.py:11 — write path looks unchanged.
