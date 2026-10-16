# Validation record

Validated on **15 October 2026 (America/Sao_Paulo)**. JSON evidence uses UTC.

Source archive SHA-256:
`c0ed6a7730e0267651479d174b9ef18ae5b580bc8ab47e1322248d60b9ca188e`

## Environment and isolation

Windows/amd64, Go 1.27.2, PowerShell 7, Docker Engine 29.8.2 / Linux containers.
Fresh Compose volumes and generated credentials were used. Existing user services
and the process occupying port 8080 were preserved. Test dependency ports:
PostgreSQL 15433, Keycloak 18081, LocalStack 14566. API_PORTS:
18080,18082,18083. Default delivery ports remain 5433/8081/4566 and
8080/8082/8083; all are configurable through the documented local workflow.

## Observed results

| Verification | Result |
|---|---|
| Source Go test baseline, without infrastructure | PASS; integration cases skipped by design in that baseline |
| Full Go suite with RUN_DB_INTEGRATION=1 and race detector | PASS; all 27 top-level tests, including 11 PostgreSQL integration tests; no race report |
| Cross-game reference rejection and outbox insertion order regressions | PASS |
| Keycloak JWT, service clients, internal access and provider isolation | PASS |
| SQS duplicate commands, durable inbox and transactional outbox | PASS |
| LocalStack pause, financial commit while unavailable, eventual publication | PASS |
| Three independent process identities and double-spend contention | PASS |
| Concurrent same-key requests and replay across processes | PASS |
| Forced stop of the second API, two-node processing and restart | PASS |
| Migrations 001–006 and global wallet/ledger reconciliation | PASS; zero divergence |
| Process identity under pt-BR, en-US and fr-FR | PASS; mismatched executable/start time refused |
| All PowerShell scripts parse; Go formatting | PASS |
| go vet ./... | PASS |
| go mod verify | PASS |
| Docker image build (Go 1.23 Alpine) | PASS; bounded build parallelism, complete log in evidence/docker-build.txt |
| Image runtime smoke checks | PASS; UID 1000, CA bundle and executable present. Full containerized API integration was not run |
| Repeated bootstrap with -Start | PASS; .env byte-for-byte unchanged and existing managed APIs reused |
| Migration preservation | Original 001–005 SHA-256 hashes match source |
| Credential hygiene | Generated local secrets absent from release sources |
| Final audit after recorded Go test rerun | Zero wallet/ledger divergence; 100 published outbox events, 0 pending/in-flight, 11 inbox records |
| Cleanup | All three audit APIs and isolated Docker services stopped; volumes retained |

Full/failover reports are in `evidence/stage8-full.json` and
`evidence/stage8-failover.json`. `evidence/verification.txt` contains selected
PowerShell verification output, with machine metadata removed. Native Go test
output is in `evidence/go-tests.txt` (a follow-up run verified complete native-output capture: 27 tests passed, zero skipped).

The initial cold Keycloak startup exceeded the original timeout. Bootstrap now
allows up to 900 seconds, verifies realm discovery/JWKS, and the API retries
transient OIDC startup failures three times. A busy 8080 was correctly refused;
configurable API_PORTS enabled validation without killing another application.

## Runtime images used

- PostgreSQL 16 Alpine: `sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea`
- LocalStack 4: `sha256:3ebc37595918b8accb852f8048fef2aff047d465167edd655528065b07bc364a`
- Keycloak 26.3: `sha256:357829ec7c4693397533035092ad13b0644bcc95ded311f33a3738c4d9e9bdba`

Compose uses version-family tags. These digests identify the tested images;
future image updates should be revalidated before use.

## Validation boundaries

This was a fresh isolated deployment on the current Windows host, not execution
on a physical second computer. API replicas ran as host Go executables, as in the
supported workflow. An API-container deployment, Linux/macOS host scripts, real
AWS SQS/IAM, production Keycloak, long-running load/partition tests and a formal
vulnerability scan were not validated. Functional tests do not prove every crash
window between SQL commit, SQS send and acknowledgement. Exactly-once transport
is not claimed. The original challenge statement was not supplied, so unseen
acceptance criteria cannot be certified.

Pre-OIDC stage 4/5 HTTP scripts and earlier stage launchers are retained as
historical resources, not current acceptance checks. The current authenticated
suite and database integration tests supersede them.

Built API image: `sha256:811477d986a754db84f543ccc12e7414ce5ed411758e5747e7ad5a0d7d3164ee`.
