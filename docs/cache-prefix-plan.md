# Cache Prefix Plan

Goal: raise provider cache hits without changing the workflow product.

## Findings

- Cache is billed in 256-token blocks; the system prompt prefix dominates a hit.
- Tool definitions do not break the cache (probe result: same system prompt with different tools still hit 67%).
- Changing the system prompt tail drops the hit rate to roughly the block containing the change (34% in the probe).
- History compaction that moves its boundary every turn rewrites the prefix; moving it in blocks of eight keeps the prefix stable for several turns.

## Adopted

- Compaction boundary advances in blocks of 8 messages.
- Tool output is bounded at the source (`read_file` 300 lines, `fetch_url` 4000 characters, patch results without diff echo).
- Read reuse: stage prompts list files earlier stages already read; repeated reads collapse to a stub.

## Rejected after measurement

| Idea | Result |
| --- | --- |
| Shared conversation across stages | cache +4 points, input +18% |
| Stable-prefix system prompt reorder | no cache gain, +40% calls |
| Per-stage output caps for action stages | truncation caused retries (45 to 94 calls) |
| Batch-read total budget | fewer lines per call increased turns |

Current hit rate on the six domains is 61-79%; the remaining miss is the new content produced each turn.
