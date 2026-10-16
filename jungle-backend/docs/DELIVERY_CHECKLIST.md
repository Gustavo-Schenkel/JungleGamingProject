# Delivery checklist

## Before handing over

- [ ] Install Docker Desktop (Linux containers), Go 1.23+ and PowerShell 7.
- [ ] Extract the ZIP to a writable folder; run commands from its root.
- [ ] Run `./scripts/bootstrap.ps1 -Start` from a new environment.
- [ ] Verify three independent processes on 8080, 8082 and 8083.
- [ ] Run `./scripts/verify.ps1`; inspect the generated JSON and console evidence.
- [ ] Optionally run `./scripts/test-stage8-db.ps1 -Race` with CGO and GCC.
- [ ] Stop processes with `./scripts/stop-stage8.ps1`.
- [ ] Confirm the release contains no `.env`, database dumps, executables, PID state or runtime logs.
- [ ] Compare the archive SHA-256 with the accompanying checksum.

This is an execution checklist for the recipient. Checked validation results for
this delivery are recorded separately in `VALIDATION.md`.

## Review scope

Financial arithmetic, concurrent wallet access, provider-scoped idempotency,
ledger reconciliation and immutability, refund/rollback rules, pending references,
OIDC verification, SQS signatures, inbox/outbox, migration serialization,
PowerShell process ownership, clean bootstrap and release hygiene.

The original challenge statement is not included in the supplied archive.
Compliance is assessed against the supplied implementation and requested scope;
unseen acceptance criteria cannot be certified.
