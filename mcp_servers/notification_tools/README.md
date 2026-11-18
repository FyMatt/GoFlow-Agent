# Generic notification connector

[简体中文](README.zh-CN.md)

`notification_tools/publish_event` is domain-neutral. Resource workflows decide
when to publish; the connector does not scan, investigate, fetch evidence, or
apply remediation. `configs/mcp_servers/notification_tools.yaml` registers this
optional stdio server. Every invocation, including local collection, advertises
`kind: network` and follows the existing tool permission and approval path.
Enabling a resource or saving a profile does not grant permission.

Arguments (unknown fields are rejected):

```json
{"channel_id":"local","severity":"warning","title":"Review required","summary":"Review the saved report.","evidence_refs":["artifact://run/report.json"]}
```

`channel_id` defaults to `local` and accepts 1-64 ASCII letters, digits,
underscores or hyphens, beginning with a letter or digit. `severity` is required:
`info`, `warning`, `error`, or `critical`. Required `title` and `summary` are
limited to 256 and 8192 UTF-8 bytes. Up to 32 optional evidence references, each
1-512 bytes, are carried as opaque strings and never fetched. Encoded delivery
payloads are capped at 32 KiB; stdio requests at 64 KiB.

The data root follows `config.ResolveDataRoot`: `GOFLOW_DATA_ROOT`, if set
(relative paths resolve against the server working directory), otherwise the
server working directory. The shipped config sets `workdir: ./`, resolved by
GoFlow against its runtime home. The environment allowlist passes the data-root
override. A custom launcher must use that same working directory or an absolute
`GOFLOW_DATA_ROOT`.

Binary releases use `GOFLOW_NOTIFICATION_TOOLS_CMD` (default:
`<runtime-home>/bin/notification_tools.exe` on Windows) through
`configs/goflow.binary.yaml`; they do not require `go run` or connector source.
The release launcher and CLI set this command; a CLI environment override is
preserved. Both connector READMEs ship alongside the optional server config.

The root is trusted user/launcher configuration, never a tool argument. Relative
roots are converted to absolute paths before use, including a user-selected
`../data` location. Outbox paths use generated 32-character hex IDs only. Reads
of profiles/secrets and outbox writes reuse the persistence relative-path
boundary and reject symbolic links or Windows directory junctions below the configured root. The root and its
parents must be user-controlled; these checks do not provide isolation against
concurrent hostile filesystem changes. Outbox writes retain the existing atomic
write-and-sync mechanism. Tokens are used only for request authentication and
are never copied into event records, results, or logs.

Records persist at `<data-root>/notifications/outbox/<event_id>.json` before any
delivery attempt. The result contains `event_id`, `channel_id`, `severity`, and
`delivery_status`, plus `http_status` when available and `error` on failure:

| delivery_status | Meaning |
| --- | --- |
| `stored` | Durable local collection only; no external delivery attempted. |
| `delivered` | Saved webhook returned HTTP 2xx; this does not attest downstream processing. |
| `failed` | Profile validation or delivery failed; MCP result has `is_error: true`. |
| `pending` | Outbox only: a delivery attempt has no persisted final outcome, possibly due to process interruption. |

No automatic retries or background worker are installed. Each invocation creates
a new persistent event ID, also sent as `Idempotency-Key`. After interruption,
inspect the outbox and destination before manually resending: pending delivery
may already have reached the destination. If final persistence fails, the tool
returns an error even if HTTP delivery succeeded; its reported delivery status
describes the observed attempt while the on-disk record can remain pending.

Only the reserved `local` collector exists by default; a profile cannot override
it. To opt into external delivery, the user saves a JSON profile at
`<data-root>/notifications/channels/<channel_id>.json` outside LLM tool arguments:

```json
{"kind":"webhook","url":"https://notifications.example.org/events","credential_ref":"secret://notifications/team-token","allow_private":false}
```

`credential_ref` is optional and must use the existing `secret://` format. The
example resolves to `<data-root>/secrets/notifications/team-token`; its content
is used as a bearer token. Inline token fields, URL userinfo, query strings and
fragments are rejected. Do not put credentials in endpoint paths. Profile and
secret files are capped at 16 KiB and 4 KiB respectively. No profile editor,
credential creation, or arbitrary URL argument is exposed as a tool.

The destination is HTTP(S); all resolved addresses are checked. Private/local
destinations require the user's explicit saved `allow_private: true`, also used
by loopback HTTP tests. Authentication requires HTTPS unless this explicit
option is enabled and all resolved addresses are private/local. The transport pins a validated address,
verifies TLS normally, disables environment proxies and redirects, and caps
response headers at 16 KiB. Response bodies are not consumed or logged. Calls
have a 10-second deadline and honor context cancellation; GoFlow's existing MCP
client terminates its child on caller cancellation. Network-disabled execution
also blocks external delivery. Errors and outbox records exclude channel URLs,
credentials, and endpoint response bodies.

Local verification (no real external messages):

```sh
go test ./internal/notification ./mcp_servers/notification_tools
```
