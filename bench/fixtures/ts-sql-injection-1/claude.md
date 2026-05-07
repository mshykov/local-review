## Critical Issues

- `src/api/users.ts:12` — **SQL injection**. The query is now built by
  template-string interpolation of `req.query.id`, which is attacker-
  controlled. Any non-numeric input (e.g. `1; DROP TABLE users;`) is
  executed verbatim. The previous code used a parameterised query with
  `$1` and `[id]` and was safe.
  *Suggested fix:* revert to parameterised form, or coerce `id` to a
  number before interpolating and reject NaN.

## Major Issues

*(None)*

## Warnings

- `src/api/users.ts:5` — `req.query.id` is typed as `string` via the
  `as` cast, but Express types it as `string | string[] | ParsedQs`.
  The cast hides the array case.

## Info / Notes

*(None)*
