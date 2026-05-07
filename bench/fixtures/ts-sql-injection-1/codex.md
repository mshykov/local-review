## Critical Issues

- src/api/users.ts:12 — SQL injection. The diff replaces the
  parameterised `$1` form with a raw template string built from
  `req.query.id`. This is exploitable by any caller. Roll back to
  the parameterised form.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

*(None)*
