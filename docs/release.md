# Release

GoFlow releases a native Windows amd64 archive. Linux code may remain for compatibility and historical testing, but Linux packages and Ubuntu acceptance are not product release targets.

## Required artifacts

- Windows amd64 archive.
- `SHA256SUMS`.
- SBOM.
- dependency license report.
- signature bundles for archives, checksums, SBOM, and the dependency license report.
- migration, capability, limitation, and recovery notes.

## Release gates

1. `go test ./...` passes.
2. JavaScript and Python syntax checks pass.
3. secret, deployment, resource-link, translation, and release-archive validators pass.
4. The Windows amd64 native build passes.
5. native install, upgrade, uninstall, crash recovery, and rollback checks pass.
6. six domain complex-task scenarios pass three consecutive times with usable artifacts.
7. desktop/mobile and light/dark browser screenshots pass review.
8. Windows native sandbox fixtures pass or the release is blocked.
9. a real vision Provider completes screenshot recognition, code localization, repair, hidden tests, and delivery writeback.
10. no high-risk unresolved dependency vulnerability remains.

## Build

```bash
python scripts/build_release_assets.py --target windows/amd64
python scripts/generate_sbom.py --version dev --update-checksums
python scripts/validate_release_archives.py --version dev --require-sbom --require-license-report
```

The formal Release job signs every archive, the SBOM, the dependency license report, and `SHA256SUMS` with the GitHub OIDC identity, then immediately runs `cosign verify-blob`. Local `dev` builds have no trusted release identity and do not generate or imitate signatures.

The release workflow must not publish container images or unsupported architectures.

After producing the Windows x64 archive, run:

```powershell
python scripts/windows_native_acceptance.py
```

This consumes the final ZIP and `SHA256SUMS` directly, then verifies installation, first start without a key, failed-upgrade protection, upgrade backup, restart, rollback, restart after rollback, another upgrade, data-preserving uninstall, and explicit data purge. Preserve its evidence under `.artifacts/windows-native-acceptance/`.
The acceptance runner invokes the installer from the final ZIP's external extraction directory, matching the supported user upgrade path. Do not execute the installer from inside the Windows installation being replaced.

Before release with a real vision Provider, run:

```bash
python scripts/real_vision_acceptance.py
```

The command generates a deterministic PNG with the Python standard library. The task prompt does not contain the file, function, port boundaries, or fallback rules shown in the image. It passes only when the Provider reads the screenshot and completes a code change, real diff, tool evidence, quality gate, public writeback, hidden tests, `go vet`, formatting, and persistence checks. A formal self-hosted runner stores the key in a read-only, non-symlink file outside the checkout and exposes only its path through the `GOFLOW_VISION_SECRET_FILE` repository variable; POSIX hosts must also deny group and other-user access. The script materializes it temporarily under an isolated `secret://` data root and deletes it afterward. `GOFLOW_VISION_PROVIDER_MODULE` points to an external vision-capable `primary` Provider module. The script copies it only into the isolated run's user-resource layer, and the module must explicitly set `vision: true` and `api_key: secret://providers/primary/api_key`. The key value must not be passed through command arguments, workflow inputs, environment values, or logs.

Formal publishing additionally requires a Windows self-hosted vision runner labeled `windows`, `x64`, and `goflow-vision-acceptance`. It uses its locally provisioned secret. A missing runner, missing capability, or failed report blocks publishing and must not be replaced by an Ubuntu runner or a skipped job.
