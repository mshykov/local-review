## Critical Issues

- `src/ui/comment.ts:6` — **Stored XSS via `innerHTML = comment.body`**.
  The diff replaces `textContent` (HTML-safe) with `innerHTML`
  (HTML-active) but pipes the comment body straight in. A commenter
  posting `<img src=x onerror="...">` runs script in the page origin.
  *Suggested fix:* keep `textContent` and render markdown server-side
  to a sanitised HTML string, or pass `comment.body` through DOMPurify
  before assigning to innerHTML.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

*(None)*
