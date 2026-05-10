# Updates

[English](./updates.md) | [简体中文](./updates.zh-CN.md)

GoFlow publishes release archives and Docker images from GitHub Actions when a
version tag is pushed. The runtime can expose update guidance through
`GET /api/update-policy` and the Studio settings page. Update checks are
explicitly opt-in: GoFlow only contacts the configured release feed when the
operator clicks the check button or calls `POST /api/update-policy/check`.

## Strategy

Update checks should be visible and operator-controlled because they contact
GitHub.

Supported install types:

- release archive: compare the embedded version with the latest GitHub Release,
  show release notes, download the matching archive only after approval, verify
  `SHA256SUMS` and Sigstore bundles, then stage replacement and restart through
  the launcher
- Docker: compare the running tag with the latest release, then show
  `docker pull` / Compose upgrade commands; the container should not replace
  itself by default
- source checkout: show `git fetch`, tag checkout, and build commands; do not
  mutate the repository unless the operator explicitly approves

## Version Source

Release archives embed the version into the `goflow` binary through:

```text
-X github.com/FyMatt/GoFlow-Agent/internal/version.Version=<tag>
```

Development builds report `dev`.

## Current Baseline

Implemented:

- `GET /api/update-policy`
- `POST /api/update-policy/check` for explicit release checks
- Studio settings page displays update strategy metadata
- Studio settings page exposes a manual update check button and renders the
  latest tag, release page, notes preview, and asset summary
- update check responses include `asset_summary`, which identifies the current
  platform archive, `SHA256SUMS`, `SBOM.spdx.json`, Sigstore bundles, and a
  `verification_ready` flag for release-archive upgrades
- `asset_summary.verify_steps` provides copyable manual checksum and Sigstore
  verification commands for the current platform; GoFlow does not run those
  commands or replace files automatically
- `GOFLOW_DISABLE_UPDATE_CHECKS=1` / `true` / `yes` / `on` hides the Studio
  check button and makes `POST /api/update-policy/check` return `403`
- release build script injects the release version into the binary

Optional follow-up:

- checksum/signature verification flow
- staged archive replacement for Windows and Linux
- Docker/source install upgrade helpers

## Release Feed Override

By default the release feed is:

```text
https://api.github.com/repos/FyMatt/GoFlow-Agent/releases/latest
```

Set `GOFLOW_RELEASE_FEED` to point at a private GitHub-compatible release JSON
endpoint for internal deployments or local tests. The check endpoint expects the
standard GitHub release fields such as `tag_name`, `html_url`, `body`, and
`assets`.

`asset_summary.expected_archive` is derived from the running platform and the
latest release tag, for example
`goflow-agent_v0.1.3_windows_amd64.zip` on Windows. `verification_ready` is
true only when the matching archive, `SHA256SUMS`, `SBOM.spdx.json`, and the
expected Sigstore bundles are all present.
`asset_summary.verify_steps` gives operator-facing commands for manual
verification. They are guidance only: GoFlow does not download, execute, or
replace release files during an update check.

## Offline Deployments

Set:

```text
GOFLOW_DISABLE_UPDATE_CHECKS=1
```

when GoFlow runs in an offline or tightly controlled environment. The settings
page still explains the update strategy, but it hides the network check button
and the check endpoint returns `403` with a clear reason.
