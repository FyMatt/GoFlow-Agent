# Research Subagent Isolation Design

## Problem

Research tasks keep every source body in one context, so each later turn re-sends all previously fetched material.

## Options

### A. Iteration node (recommended)

The workflow graph supports `for_each`:

```yaml
- name: per-source
  node_type: for_each
  params:
    stage: fetch-source
    items_ref: stages.catalog.outputs.urls
```

Each iteration runs with its own context; the parent only receives structured fields (id, url, status, published_at, sha256, short excerpt).

### B. New subagent primitive

Add `agent_tools/spawn_subagent` with an isolated context and a structured result. More general, but requires a new tool, budget propagation and tests.

## Recommendation

Start with A for the research domain. Measurements show the report stage accounts for ~61% of that domain cost while fetching is not the bottleneck, so validate the gain before investing in B.
