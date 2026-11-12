# Updates

Update checks are opt-in and use the configured GitHub release feed only after the user requests a check.

GoFlow supports release-archive and source-checkout update guidance. A release-archive update must verify checksums and signatures, create a data backup, stage replacement files beside the current installation, and restart through the launcher. Source checkouts require explicit approval before repository mutation.

Automatic update logic must never overwrite a running installation without a recoverable staged replacement.
