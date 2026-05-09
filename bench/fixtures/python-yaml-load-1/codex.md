## Critical Issues

- app/config.py:8 — yaml.load without an explicit SafeLoader allows
  arbitrary Python class instantiation via !!python/object tags.
  Restore yaml.safe_load.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

- app/config.py:13 — write path uses safe_dump, which is fine.
