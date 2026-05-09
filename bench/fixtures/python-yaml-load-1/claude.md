## Critical Issues

- `app/config.py:8` — **`yaml.load(..., Loader=yaml.UnsafeLoader)` is
  arbitrary-code-execution-class.** UnsafeLoader resolves
  `!!python/object/apply` tags, so a config file containing
  `!!python/object/apply:os.system ["rm -rf ~"]` runs the command
  on load. This is the canonical PyYAML deserialization-CVE shape
  (CVE-2017-18342 et al.). The pre-diff `yaml.safe_load(f)` was the
  safe form — revert, or pass `Loader=yaml.SafeLoader` explicitly.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

*(None)*
