# local-review bench leaderboard

_Generated 2026-05-09 07:25 UTC_ · _Dataset: bench/dataset (10 cases)_ · _Mode: replay_

## Overall

| LLM | Precision | Recall | F1 | Noise | Cons. | Median | P95 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| claude | 0.75 | 1.00 | 0.86 | 0.00 | — | 0ms | 0ms |
| codex | 0.62 | 0.89 | 0.73 | 0.00 | — | 0ms | 0ms |
| gemini | 0.33 | 0.89 | 0.48 | 1.00 | — | 0ms | 0ms |

## Per-language F1

| LLM | go (4) | python (2) | rust (1) | typescript (3) |
| --- | --- | --- | --- | --- |
| claude | 0.89 | 1.00 | 0.67 | 0.80 |
| codex | 0.75 | 0.67 | 0.67 | 0.80 |
| gemini | 0.46 | 0.50 | 0.50 | 0.50 |

## Per-case detail

| Case | Lang | claude | codex | gemini |
| --- | --- | --- | --- | --- |
| clean-go-rename-1 | go | F1=0.00 | F1=0.00 | F1=0.00 |
| clean-ts-import-reorder-1 | typescript | F1=0.00 | F1=0.00 | F1=0.00 |
| go-error-shadow-1 | go | F1=1.00 | F1=1.00 | F1=0.50 |
| go-nil-deref-1 | go | F1=1.00 | F1=0.50 | F1=0.40 |
| go-race-mapwrite-1 | go | F1=0.67 | F1=1.00 | F1=0.50 |
| python-shell-injection-1 | python | F1=1.00 | F1=0.67 | F1=0.50 |
| python-yaml-load-1 | python | F1=1.00 | F1=0.67 | F1=0.50 |
| rust-unsafe-deref-1 | rust | F1=0.67 | F1=0.67 | F1=0.50 |
| ts-sql-injection-1 | typescript | F1=0.67 | F1=1.00 | F1=0.50 |
| ts-xss-innerhtml-1 | typescript | F1=1.00 | F1=0.67 | F1=0.50 |
