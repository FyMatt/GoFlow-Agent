# Web Studio Frontend Optimization Plan

[English](./frontend-optimization-plan.md) | [简体中文](./frontend-optimization-plan.zh-CN.md)

This document records the measured baseline, conclusions, and staged plan for the
Web Studio frontend (`internal/api/web/`). The goal is to make this local-first
agent workflow platform hold up on "convenient and pleasant to use": faster first
paint, interactions that do not lose state, maintainable styling, and no further
accumulation of dead assets.

## 1. Conclusion First

**Do not replace the frontend framework.** Keep the existing vanilla ES module
rendering model and add the three things it is missing: transport compression,
DOM diffing, and a style system.

The reasoning is measured, not preference:

- `internal/api/web/assets/views/workflows.js` is 15,162 lines and 811 functions
  in a single file -- a complete canvas editor. Rewriting it in Vue/React/Svelte
  is quarter-scale work at high risk.
- `assets/app.js` (341 lines) is decent as-is: hash routing, dynamic `import()`
  code splitting, `showSequence` race guarding, scroll-intent tracking. The shell
  is not where the problem lives.
- The view layer is already **hand-rolling a diff**. For example
  `views/workflows.js:5331`:
  `if (select.innerHTML !== nextHTML) select.innerHTML = nextHTML;`
  The name `nextHTML` recurs at 1972, 2318, 3120, and 5331 -- string-level dirty
  checking. That says the need is a real diff, not a new framework.

## 2. Measured Baseline

Data below comes from the current working tree. The asset-audit portion is
reproducible via `.tmp/console_audit.py`.

### 2.1 First-paint transfer

`web/console.html` synchronously loads two stylesheets plus `i18n.js` before any
view module starts loading:

| Asset | Raw | gzip | Ratio |
|---|---|---|---|
| `assets/styles.css` | 477,876 B | 61,357 B | 12.8% |
| `assets/styles-workbench.css` | 91,075 B | 14,636 B | 16.1% |
| `assets/i18n.js` | 550,626 B | 139,421 B | 25.3% |
| `assets/views/workflows.js` | 768,416 B | 146,701 B | 19.1% |
| `assets/views/chat.js` | 433,451 B | 86,009 B | 19.8% |

**Key finding: neither `internal/api/api.go` nor `internal/api/console.go` has
gzip middleware, and there is no `Content-Encoding` implementation anywhere under
`internal/api/`.** The "Raw" column above is what actually goes over the wire --
roughly 1.18 MB for first paint. This is the highest-leverage item and needs only
the standard library.

### 2.2 JavaScript size

```
assets/views/workflows.js   15,162 lines   811 functions
assets/views/chat.js         9,289 lines
assets/i18n.js               7,684 lines   3,043 unique keys / 6,265 language copies
assets/views/settings.js     2,010 lines
assets/views/catalog.js      1,093 lines
assets/api.js                  678 lines
assets/app.js                  341 lines
total                       37,060 lines
```

### 2.3 Focus/scroll rescue code

Rewriting a whole subtree with `innerHTML` destroys input elements, so the views
manually save and restore caret and scroll position:

| File | `activeElement`/`selectionStart`/`scrollTop` | `innerHTML` |
|---|---|---|
| `views/chat.js` | 28 | 73 |
| `views/workflows.js` | 15 | 93 |
| `views/catalog.js` | **0** | 5 |
| `views/settings.js` | **0** | 7 |

`catalog.js` and `settings.js` use the same `innerHTML` pattern with zero rescue
code, so input controls there are likely losing focus and caret position on
re-render (inferred statically, not reproduced in a browser).

### 2.4 CSS layering history

`assets/styles.css` is 19,665 lines and ends with 10 redesign layers that were
**appended** rather than merged:

```
16480  /* GoFlow visual refresh: dense, technical Agent operations surface. */
17524  /* Product workbench refresh: lower the AIGC feel... */
17802  /* Workflow side panel and runtime preview refinements. */
18290  /* Workflow run preview... */
18683  /* Unified workbench pass: focused defaults and calmer light/dark themes. */
19024  /* Run page and sidebar correction... */
19239  /* Workflow studio final pass... */
19321  /* EOF runtime highlight override: keep current node obvious after all theme passes. */
19454  /* Light theme final-output readability. */
19527  /* Runtime/current node polish. Kept at EOF to override older emphatic runtime styles. */
```

Consequences:

- 1,075 `!important` declarations (`styles.css` 604 + `styles-workbench.css` 471)
- `:root` defined 5 times, `:root[data-theme='dark']` 5 times, `.sidebar` 4 times,
  `.flow-node` 4 times
- `--radius` redefined three times (12px -> 8px -> 8px); `--accent` defined 4 times
  across two files
- No type scale: font-size 12px x214, 11px x178, 10px x79, 13px x62, plus
  9px/10.5px/11.5px/14.5px one-offs
- No spacing tokens at all (`--space-*` does not exist)
- 48 media queries across 19 distinct breakpoints (600/640/700/720/760/860/880/
  900/980/1080/1100/1120/1180/1200/1201/1280/1360/1500/1760), mixing 760 with 700
  and 900 with 880

### 2.5 Real defects caused by the layering

1. **Dark-mode runtime node highlight is broken.** The winning palette lives in
   `styles-workbench.css` (loaded last), where `--blue: #ececec` in dark mode. The
   highlight block starting at `styles.css:19527` builds its border, outline, and
   box-shadow entirely from `var(--blue)`, so in dark mode it is near-white and
   hard to tell apart from plain hover/selected. The adjacent `runtime-waiting`
   state uses real amber `--amber`, which makes "waiting" more prominent than
   "currently running" -- **inverted semantics**.
2. **Semantic colors desaturated wholesale.** `--blue`/`--purple`/`--cyan`/
   `--teal`/`--brand` collapse to grays across the layers, leaving only
   `--green`/`--red`/`--amber` able to carry meaning.
3. **Inter never loads.** `--font-sans: Inter, ...` is declared, but there is no
   `@font-face` anywhere in the project and no font link in `console.html`. Only
   users with Inter installed locally see the intended design.
4. **Dead animation code.** `portPulse` mixes `var(--blue)` with a hardcoded
   legacy blue `rgba(37, 99, 235, .20)`. The two carefully gated
   `prefers-reduced-motion: no-preference` node-pulse blocks at `styles.css:18659`
   and `19399` are fully overridden by `animation: none !important` in the 19527
   layer.

### 2.6 Dead asset audit

The audit script must account for dynamically constructed keys or it produces
false positives. `resource_actions.js:82-105` builds a key into a variable before
calling `t()` (`` const kindKey = `resource.action.${kind}.${actionName}` ``), and
`actionName` comes from the backend `/api/capabilities`. Matching only inline
`` t(`...${ `` misreports 26 live `resource.*` keys as dead. Corrected results:

| Namespace | Unique keys | Used | Dead |
|---|---|---|---|
| `approvals` | 190 | 11 | **179** |
| `workspace` | 126 | 5 | **121** |
| `tour` | 77 | 0 | **77** |
| `chat` | 694 | 659 | 35 |
| `workflow` | 1463 | 1428 | 35 |
| `settings` | 345 | 312 | 33 |
| `common` | 25 | 16 | 9 |
| `runtime` | 10 | 1 | 9 |
| `catalog` | 55 | 54 | 1 |
| `resource` | 26 | 26 | 0 |
| `nav` / `view` / `theme` / `brand` | 32 | 32 | 0 |

Also:

- **Icons**: `assets/icons.js` defines 43; 10 have no reference outside icons.js:
  `book-open`, `chevrons-left`, `layout-grid`, `layers`, `library`, `maximize-2`,
  `list-todo`, `plug`, `users`, `wrench`
- **CSS classes**: 1,177 distinct class names, 245 of which never appear in live
  JS/HTML. Every `classList.add/toggle` call was verified to use literal strings,
  so "never appears" means genuinely dead.
- **The tour is orphaned**: its driver `assets/onboarding.js` was already deleted
  in the working tree, but roughly 30 `data-tour-id` anchors, 77 `tour.*` keys,
  77 `styles.css` references and 3 workbench references remain.
- The views deleted in the same batch -- `approvals.js`, `memory.js`,
  `overview.js`, `status.js`, `workspace.js` -- left behind the `approvals.*` and
  `workspace.*` dead keys in the table above.

## 3. Staged Plan

### Stage 0: Stop the bleeding (this stage; low risk, high return)

| # | Item | Expected effect |
|---|---|---|
| 0.1 | Standard-library gzip middleware (`console.html` + `/assets/*`) | first paint ~1.18 MB -> ~230 KB |
| 0.2 | `ETag` + `If-None-Match` conditional requests for static assets | 304 on repeat visits |
| 0.3 | Switch to system-ui first, drop the never-loaded Inter | removes cross-machine type drift |
| 0.4 | Add a `--runtime-active` semantic token so the highlight no longer derives from `--blue` | fixes inverted dark-mode semantics |
| 0.5 | Remove `portPulse`'s hardcoded legacy blue and the overridden `no-preference` blocks | deletes dead code |
| 0.6 | Remove the whole tour chain (i18n keys, CSS, `data-tour-id`) | orphaned feature fully removed |
| 0.7 | Delete `approvals.*`/`workspace.*` dead keys and 10 unused icons | i18n and icon slimming |
| 0.8 | Keep `.tmp/console_audit.py` as a re-runnable audit | prevents dead assets from re-accumulating |

### Stage 1: Build layer + DOM diff (about 1 week)

- Adopt esbuild (a single Go binary, wires into `go generate` and existing CI with
  no runtime node_modules): bundle, minify, per-view chunks.
- Adopt **morphdom** (~2 KB, vendored into the repo, no node_modules) to replace
  the `innerHTML` assignments in the four views: `el.innerHTML = h` becomes
  `morph(el, h)`. View code needs no changes, the 43 focus/scroll rescue blocks
  can be deleted, and the latent focus loss in `catalog.js`/`settings.js` is fixed
  along the way.
- Move new code to **uhtml** (~2.5 KB) over time: its interpolations escape by
  default, replacing the `escapeHTML()` calls that a human currently has to
  remember at 178 `innerHTML` sites. This platform renders LLM output, file
  contents, and command echoes; one miss is an XSS.
- Add frontend tests: `api.js`, `approval_counts.js`, and `run_context.js` are
  pure-logic modules, the easiest and most worthwhile to cover. Frontend test
  coverage is currently zero while backend coverage is decent -- an inverted ratio.

**Dependency stance**: `go.mod` is 227 bytes of pure standard library, and that
restraint is an asset. Third-party frontend libraries are therefore vendored as
single files into `assets/`, keeping clone-then-`go build` intact.

### Stage 2: CSS consolidation (1-2 weeks; prerequisite for all later polish)

- Merge the 5 `:root` blocks into a single token source; add `--space-*` and
  `--text-*` scales.
- Replace the 1,075 `!important` contests with `@layer` (base / components / theme).
- Collapse 19 breakpoints down to 4-5.
- Remove the 245 dead CSS classes from section 2.6.

### Stage 3: Splitting and visual polish (ongoing)

- Split `views/workflows.js` by responsibility (canvas / inspector / runtime /
  palette / edges), paired with esbuild chunking.
- View transitions, list skeletons (`skeleton`/`shimmer` currently match 0 times),
  unified toast feedback (currently 2 matches).
- Unify the icon API: `icon()` hardcodes `width="16"`, and `viewIcon()` returns a
  string while the other three return markup.
- Make `brand-mark.svg` theme-aware: it currently hardcodes cream `#E8E2D4`, which
  is jarring in dark mode.

## 4. Open Decisions

- **Type density**: currently 12px x214, 11px x178, 10px x79, with only 12
  declarations above 16px. If this "technical tool" density is intentional, the
  scale gets built on that baseline; bumping everything up one step would visibly
  improve readability and accessibility but affects every existing layout. Needs a
  decision before Stage 2.
- **Inter**: Stage 0 switches to system-ui. Restoring Inter later means
  self-hosting and subsetting, adding roughly 100-300 KB to the binary.

## 5. Verification

- `go build ./...` and `go test ./internal/api/...`
- gzip and ETag behavior covered by `internal/api/console_compression_test.go`
- `python .tmp/console_audit.py` re-runs the asset audit; dead keys and dead icons
  should report 0
