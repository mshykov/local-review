## Critical Issues

- src/ui/comment.ts:6 — assigning user-controlled `comment.body` to
  `innerHTML` is a stored XSS sink. Sanitise via DOMPurify or revert
  to `textContent`.

## Major Issues

*(None)*

## Warnings

- src/ui/comment.ts:1 — `comment.author` is built into the DOM
  elsewhere; verify it's similarly handled.

## Info / Notes

*(None)*
