# Releasing shell3

Review every commit since the latest published release, including compatibility,
durable state, cancellation, and transport failure paths. Fix release blockers on
a `fix/` or `feature/` branch from main, verify, squash to main, and remove the
finished branch. Do not create release or backup branches.

Before tagging, run the required build/tests/lint plus the full race suite,
`make acceptance`, and the deeper analyses in `scripts/deepcheck.sh`. Confirm the
source tree is clean, dependencies are unchanged, and the intended commit passed
CI. Document remaining operational limitations rather than equating passing tests
with proof of every production behavior.

Write reviewed user-facing notes in `docs/releases/vX.Y.Z.md`. Include behavior
changes, upgrade/rollback constraints, and the complete previous-release range.
The release workflow requires these notes and reuses the full CI workflow as a
prerequisite. GoReleaser produces a draft only after those gates pass and verifies
module contents without running `go mod tidy` during assembly.

Publishing requires explicit user authorization. Push main normally, wait for CI,
then push an annotated version tag at that exact commit. Wait for the release
workflow; inspect the draft's notes, all four OS/architecture archives, checksum
manifest, and embedded version. Download and checksum the artifacts; smoke-test
the native binary and run local acceptance against it before publishing the
draft. Never move or force-push a published tag to repair a release.

Verify each archive checksum and inspect `go version -m` on its binary: the
revision must match the tag and `vcs.modified` must be `false`. Keep GoReleaser's
`dist/` output ignored so generated artifacts cannot dirty the source stamp.

A public release does not itself replace any running installation. Deployment
requires its own authorized quiet window, coherent binary/configuration changes,
rollback preparation, and observation after startup.
