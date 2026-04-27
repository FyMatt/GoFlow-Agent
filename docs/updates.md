# Updates

GoFlow publishes release archives and Docker images from GitHub Actions when a
version tag is pushed. The runtime can expose update guidance through
`GET /api/update-policy` and the Studio settings page.

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
- Studio settings page displays update strategy metadata
- release build script injects the release version into the binary

Remaining:

- direct GitHub Releases check endpoint with opt-in network access
- checksum/signature verification flow
- staged archive replacement for Windows and Linux
- Docker/source install upgrade helpers
- config switch to disable update checks in offline deployments
