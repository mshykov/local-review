## Critical Issues

- app/config.py:8 — yaml.load with Loader=yaml.UnsafeLoader allows
  arbitrary Python class instantiation via !!python/object and
  !!python/object/apply tags. A malicious config can run code on
  load. Restore yaml.safe_load.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

- app/config.py:13 — write path uses safe_dump, which is fine.
