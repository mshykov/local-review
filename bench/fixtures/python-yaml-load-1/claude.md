## Critical Issues

- `app/config.py:8` — **`yaml.load(f)` without `Loader=SafeLoader` is
  arbitrary-code-execution-class**. Older PyYAML resolves the no-Loader
  call to `FullLoader`/unsafe Loader; either way, a config file that
  contains `!!python/object/apply:os.system [...]` runs the command on
  load. The previous `yaml.safe_load(f)` was the safe form — revert,
  or pass `Loader=yaml.SafeLoader` explicitly.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

*(None)*
