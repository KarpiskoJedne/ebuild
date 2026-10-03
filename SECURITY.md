# Security policy

## Hermetic build guarantees

- Build actions execute with loopback-only networking (Linux network
  namespaces, Windows Job Objects/AppContainer, macOS Seatbelt).
- Host environment variables are stripped; only explicitly declared inputs
  enter the action environment (see `internal/core/toolchain.go`).
- Every artifact is content-addressed (BLAKE3, SHA-256 fallback) and recorded
  with SLSA provenance; per-target CycloneDX/SPDX SBOMs are emitted by
  `internal/core/sbom.go`.

## Reporting vulnerabilities

Email **security@ebuild.dev** with a reproducer and impact assessment.
We acknowledge within 2 business days and target a fix + advisory within
30 days. Do not open public issues for unpatched vulnerabilities.

## Supply chain

- Toolchains are pinned (`.tool-versions` / `ebuild.yml`) and SHA-256
  verified before first use.
- Provenance is attested via Sigstore/cosign at SLSA Level 4 tooling
  expectations: hermetic, isolated, and parameter-complete.
