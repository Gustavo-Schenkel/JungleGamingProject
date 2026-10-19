# Jungle Gaming — Transactional Go Backend

A wagering backend built with Go, Uber Fx, PostgreSQL, Keycloak OIDC and SQS FIFO.
It supports exact monetary arithmetic, persistent idempotency, an append-only
ledger, refunds, rollbacks, delayed references, and three independent API processes.

## Requirements

- Windows with PowerShell 7 (`pwsh`), Go 1.23 or later, and Docker Desktop using Linux containers.
- Docker Compose v2; internet access for images and Go modules on first run.
- Free host ports: 5433 (PostgreSQL), 8081 (Keycloak), 4566 (LocalStack), and 8080/8082/8083 (APIs).
- Optional: CGO-compatible GCC on PATH for Go's race detector. Standard tests do not require GCC.

## Run from a clean machine

**FIRST OF ALL, CREATE A .env NOTE IN jungle-backend FOLDER WITH .enx-example AS YOUR BASE; JUST CHANGE POSTGRES USER/PASSWORD AND KEYCLOAK ADMIN/PASSWORD AS YOUR OWN CREDENTIALS. DO NOT EXCLUDE .env-example!!



Open PowerShell 7 in the project root: the folder containing `go.mod`, `docker-compose.yml` and `scripts`. Run bootstrap:

```powershell
./scripts/bootstrap.ps1 -Start
./scripts/verify.ps1
```

Bootstrap creates `.env` from `.env.example`, generates random database/admin
passwords, starts the three Docker dependencies, waits for readiness, provisions
the `jungle` realm and three confidential service clients with the API audience,
creates the FIFO queues and DLQ, and launches three API processes. Migrations
001–006 run automatically before the API listens. No database backup is needed.
First startup may take several minutes. Failed readiness produces an error;
inspect `docker compose logs`, then rerun bootstrap. Existing volumes and local
credentials are retained. Never share `.env`.

If PowerShell blocks a downloaded script, review it and use `Unblock-File` on the
reviewed scripts. Do not change the machine-wide execution policy.

For existing services occupying the default ports, create `.env` from the example
before bootstrap and adjust matching pairs: POSTGRES_PORT/POSTGRES_COMPOSE_PORT,
KEYCLOAK_PORT/OIDC_ISSUER, and LOCALSTACK_PORT/SQS_ENDPOINT. Use unique container
names and COMPOSE_PROJECT_NAME for a separate deployment. Set API_PORTS to three free ports (default 8080,8082,8083); the verification workflow follows this setting. Unknown listeners cause
startup to stop; no existing process is killed.

## Local configuration and existing installations

Ship `.env.example`, not the local `.env`. On a clean machine bootstrap creates
`.env`, generates passwords, provisions dependencies and applies migrations.
Keep the generated `.env` with its Docker volumes: existing databases retain their
original passwords. Bootstrap adds missing release settings to older stage `.env`
files without replacing existing credentials.

Names, ports and volumes must identify one installation consistently. Changing
the extraction folder alone does not isolate Docker container names or ports.
To create an independent local installation when defaults are occupied:

```powershell
./scripts/bootstrap.ps1 -InstanceName jungle-reviewed -PortOffset 20000 -Start
```

This example uses project/volume prefix `jungle-reviewed`, dependency ports
25433/28081/24566 and API ports 28080/28082/28083. Choose a different unused name
and offset if those are occupied. These settings are saved in `.env`; subsequent
runs use only `./scripts/bootstrap.ps1 -Start`. Stop this installation's APIs
before changing its profile. No other installation is stopped or removed.

`./scripts/bootstrap.ps1 -Start -CheckOnly` prepares local configuration and checks
Compose, container ownership and requested ports without starting containers.
Bootstrap reports conflicts before startup and waits up to 900 seconds per
dependency for readiness. Queue provisioning has a separate three-minute deadline
for cold SQS initialization. Failed startup must be resolved before verification.

## Verify and inspect

After `Bootstrap complete` appears, run the full verification suite:

```powershell
./scripts/verify.ps1
```

The verifier checks that all three managed APIs are ready before running tests.
Go test packages run sequentially; the concurrent requests inside each test are
unchanged. This avoids unnecessary contention between packages on a local VM.

`verify.ps1` runs database tests, OIDC/provider isolation, SQS duplicate handling,
a temporary outage of this deployment's LocalStack, three-process concurrency,
failover/restart of port 8082, ledger reconciliation and Go static analysis.
It creates fresh test records and retains them for audit. Run it only against a
local development deployment. If a check fails, the script stops with an error.

```powershell
./scripts/test-stage8-db.ps1 -Race  # optional; requires GCC and CGO_ENABLED=1
./scripts/check-stage8.ps1
Invoke-RestMethod http://127.0.0.1:8080/health/ready
```

Evidence is generated under `.stage8-evidence/`: console transcript and separate
JSON reports for full and failover verification. Process metadata and API logs
are under `.stage8-run/`. Compiled executables are under `.stage8-bin/`.
None belongs in a source release. Observed delivery results are in
[docs/VALIDATION.md](docs/VALIDATION.md); historical stage notes are not proof
that this release passed.

## API and authentication

| Method / path | Access | Purpose |
|---|---|---|
| GET /health/live | Public | Process liveness |
| GET /health/ready | Public | PostgreSQL availability |
| POST /v1/transactions | Provider token | Execute a wagering command; Idempotency-Key required |
| GET /v1/transactions/{externalTransactionId} | Provider token | Lookup scoped to the token's provider |
| POST /internal/dev/wallets/open | Internal token, development, loopback | Create funded wallet and opening ledger |
| POST /internal/wallets | Internal token, development, loopback | Create zero-balance wallet |
| GET /internal/wallets/{id} | Internal token, development, loopback | Inspect wallet |
| GET /internal/dev/wallets/{id}/ledger | Internal token, development, loopback | First 100 ledger entries |
| GET /internal/dev/messaging/status | Internal token, development, loopback | Inbox/outbox diagnostics |

Clients: `jungle-provider-alpha` → `provider-alpha`, `jungle-provider-beta` →
`provider-beta`, and `jungle-internal`. Client secrets are generated locally and
stored in `.env`. Tokens use client_credentials against
`$OIDC_ISSUER/protocol/openid-connect/token`, with audience `jungle-api`.
Provider identity is derived from the verified token; a conflicting providerId
in JSON returns 403. Internal tokens cannot submit provider transactions.
`test-stage6.ps1` provides a complete authenticated request example.

Example transaction body (replace identifiers with a funded wallet):

```json
{
  "externalTransactionId": "bet-001",
  "playerId": "player-001",
  "walletId": "00000000-0000-4000-8000-000000000001",
  "roundId": "round-001",
  "gameId": "game-001",
  "kind": "BET",
  "money": { "amount": "10.00", "currency": "BRL" }
}
```

Send `Authorization: Bearer <access token>` and `Idempotency-Key: bet-001`.
Amounts are JSON strings with up to two decimal places, never floats.
Kinds: BET, WIN, LOSS (zero), REFUND and ROLLBACK. Reversals require
`referenceExternalTransactionId`. Results include transactionId, status, balance,
walletVersion and optional failureCode. HTTP 200 covers processed and pending
results; 422 is a persisted business rejection; 400 invalid input; 401/403 auth;
404 missing wallet/transaction; 409 conflicting key or external ID; 500 unexpected
failure. Replay the identical command/key after an uncertain network outcome.
Opening wallet creation is not an idempotent wagering operation: duplicate
player/currency returns 409. A pending reference may resolve after initial reply.

SQS producers use `cmd/sqs-send` after loading `.env`; commands use schemaVersion
1, clientId, idempotencyKey, command and a provider-specific HMAC signature.
The outgoing event contains eventId, walletId, kind, status, balanceMinorUnits and
walletVersion. Delivery is at-least-once: consumers must deduplicate eventId.

## Stop and restart

```powershell
./scripts/stop-stage8.ps1
# From the project root; stops only this Compose deployment, retaining volumes:
docker compose stop
# Later:
./scripts/bootstrap.ps1 -Start
```

Do not use `docker compose down -v` unless you explicitly intend to erase this
deployment's database, Keycloak and queue data. Editing POSTGRES_PASSWORD does
not change the password in an existing database volume. Changing a Keycloak
bootstrap password does not reset an existing administrator account; use the
Keycloak admin console/account settings. After deliberate service-client secret
rotation, update `.env` consistently and rerun setup-keycloak.ps1. HMAC key changes
can invalidate queued commands signed with old keys: drain/coordinate rotation.

## Troubleshooting

- Docker connection error: start Docker Desktop and confirm Linux containers.
- Port in use: free it deliberately or adjust the matching infrastructure settings above.
- Keycloak timeout: inspect logs; first-run configuration can be slow on constrained hosts. Bootstrap accepts `-ServiceTimeoutSeconds 900`.
- Database authentication error: confirm the existing volume's user/password; bootstrap never overwrites it.
- API startup failure: inspect `.stage8-run/api-<port>.stderr.log`; verify realm audience, generated secrets, queues and ports.
- PID ownership mismatch: inspect the recorded process and path. Do not stop a reused PID; the scripts refuse it.
- Race detector error: install GCC, enable CGO, and rerun with `-Race`. Record unavailability rather than treating ordinary tests as race validation.

## Project map and limitations

`cmd/api`: Fx entry point; `internal/finance`: transactional domain;
`internal/money`: cents arithmetic; `internal/auth`: JWT/JWKS;
`internal/messaging`: HMAC, FIFO inbox/outbox; `internal/database`: embedded SQL;
`scripts`: bootstrap and tests; `docs`: audit, checklist and validation.

See [ARCHITECTURE.md](ARCHITECTURE.md), [docs/AUDIT.md](docs/AUDIT.md),
[docs/DELIVERY_CHECKLIST.md](docs/DELIVERY_CHECKLIST.md) and
[docs/VALIDATION.md](docs/VALIDATION.md). Earlier stage scripts/documents are kept
for traceability; pre-OIDC stage 4/5 HTTP scripts are historical and are superseded
by the authenticated current suite. The supported local topology runs Go on the
host. The Dockerfile builds an API image with bounded compilation parallelism, but Compose does not deploy API replicas.
Production rollout requires separate TLS, least-privilege roles/IAM, secrets
management, metrics, DLQ operations, and deployment/network design.
