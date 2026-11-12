# Acceptance Status (2026-11-12)

## Environment

- Build: this repository (single squashed commit on `master`)
- Service: `goflow.exe --config configs/goflow.yaml --workspace <workspace> --http 127.0.0.1:8080`
- Provider: DeepSeek `deepseek-flash` (`configs/providers/primary.yaml`)
- Harness: `scripts/p7_provider_runner.py` with `scripts/kit_acceptance.py` in strict mode

## Six-domain acceptance (same build, strict kit acceptance)

| Domain | Calls | Cache | Miss | Output | Off-peak cost | Kit | Result |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Cross-domain | 18 | 79% | 26.6k | 11.4k | 0.07 | pass | pass |
| Development | 40 | 61% | 196.7k | 31.2k | 0.33 | pass | pass |
| Documents | 20 | 65% | 69.9k | 14.3k | 0.13 | pass | pass |
| Operations | 24 | 72% | 49.2k | 16.9k | 0.12 | pass | pass |
| Research | 19 | 64% | 77.4k | 26.2k | 0.19 | pass | pass |
| Security | 33 | 63% | 128.8k | 32.5k | 0.26 | pass | pass |
| **Total** | **154** | - | - | - | **1.10** | 6/6 | **6/6** |

Evidence directories: `.artifacts/p7-provider/20261112-*/report.json` (per-stage usage plus kit acceptance output).

## Engineering regression

`go vet ./...`, `go test ./... -count=1`, `gofmt -l cmd internal pkg mcp_servers` and Python `py_compile` all pass.

## Product goals

| Goal | Status | Evidence |
| --- | --- | --- |
| Resources as components | 11 kits, each with routing and acceptance | `GET /api/kits` |
| Workflow page | Built-in templates, editable and validated | `/workflows` returns 200 |
| Task page infers resources | Preflight selects a kit entry; unmatched change requests fall back to a write-capable agent | `POST /api/runs/preflight` |
| Run observability | Stages, evidence and cost visible | `/console`, `/workspace`, `/api/runtime/cost` |
| Cost control | 1.10 CNY for six domains off-peak | table above |

## Remaining tasks

| Item | Status |
| --- | --- |
| Six-domain re-run after the latest changes | pending |
| Controlled vulnerability positive/false-positive acceptance | done (`scripts/security_detection_acceptance.py`) |
| Run-completion email notification | implemented (SMTP) |
| Windows release archive rebuild | pending (`dist/` is stale) |
| Manual acceptance walkthrough | to be performed by the user |
| Thesis rewrite | owner responsibility |
