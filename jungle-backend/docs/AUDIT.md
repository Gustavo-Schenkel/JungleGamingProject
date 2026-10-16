# Source audit and remediation

Source: `jungle-backend-challenge-stage8-distributed(1).zip`.
Historical stage documents describe prior work and are not execution evidence for
this release. See `VALIDATION.md` for observed results.

| Area | Review outcome / change |
|---|---|
| Money | Decimal strings converted to int64 cents; overflow and currency mismatch checked. All currencies use two decimal places by contract. |
| Wallet concurrency | PostgreSQL wallet row lock serializes movements across processes; no in-memory balance authority. |
| Idempotency | Provider/key and provider/external ID unique constraints; canonical payload hash rejects conflicting reuse. Replays reuse persisted results. |
| Reversals | Full amount, provider, wallet, player, round and currency checked; added missing game match. Unique partial index prevents two completed reversals of one reference. |
| Ledger | Balance and ledger commit together; SQL guards forbid ledger UPDATE, DELETE and TRUNCATE for normal operations. Database owners can still alter guards. |
| Pending references | Durable retries and capped backoff; ends after 12 attempts or deadline, whichever occurs first. No movement before resolution. |
| OIDC | RS256/JWKS, issuer, audience, token type and time claims; client allowlist derives provider identity. Development retains authentication. |
| SQS inbound | HMAC per provider; inbox and financial commit share a transaction. Receive one message at a time so a local batch cannot outlive its visibility window before processing starts. |
| SQS outbound | Lease token fences stale acknowledgements; crash after send can cause redelivery. Added migration 006 with insertion sequence: transaction-start timestamps could invert wallet event order. |
| Bootstrap | Fresh .env generation, random local passwords, Compose checks, readiness polling, Keycloak clients/audience and FIFO queues. No prior-stage .env required. Realm discovery/JWKS warmed before API launch; transient OIDC startup failures retry three times without relaxing token verification. |
| Docker | Loopback bindings, persistent Keycloak volume, configurable names/ports, no Docker socket mount, go.sum copied for build, CA certificates in runtime. |
| Processes | Executable path plus invariant UTC start ticks verify PID identity; state saved after each launch; unknown processes are never stopped. |
| Portability | Removed fixed infrastructure ports from current stage harness and active tests; removed mandatory machine-specific GCC path from current DB runner. |
| Package | Excluded original DB dump, binaries, PID files, runtime evidence and private configuration. Source 001–005 migrations preserved byte-for-byte. |

## Deliberate boundaries

- Supported delivery path: host Go processes plus Docker dependencies, Windows / PowerShell 7. The Dockerfile is an alternative API image, not a fully containerized development topology: localhost OIDC/SQS and loopback internal routes assume host execution.
- FIFO and PostgreSQL idempotency provide one financial effect per accepted business operation; transport is at-least-once. Downstream consumers must deduplicate by eventId, including beyond SQS's deduplication window.
- Outbox ordering assumes events are inserted through the financial service while holding the wallet lock. Direct SQL writers must follow the same contract.
- A WIN without a reference remains supported, as in the approved source. A LOSS is a zero-value persisted result without a ledger movement. Refunds are full, not partial; a failed rollback never forces a negative balance.
- Database isolation among providers applies to transaction identity and lookup. Wallets belong to players, and authorized providers can transact against a supplied player/wallet pair; there is no player-to-provider ownership model in this challenge.
- Readiness probes test PostgreSQL at request time. OIDC and SQS are checked during startup; their continuous availability is not implied by a ready response.
- Ledger endpoint returns at most 100 entries; use SQL audit views for complete reconciliation.
- Use wallet IDs exactly as returned by the API (canonical lowercase UUIDs). Request hashing does not normalize UUID letter case.
- No production certification, prolonged load/partition testing, external AWS validation or formal security audit is implied. Custom JWT verification has targeted tests but remains a maintenance responsibility.
