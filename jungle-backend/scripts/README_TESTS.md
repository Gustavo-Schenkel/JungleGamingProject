> Historical stage reference. For the supported delivery workflow and current results, read README.md and docs/VALIDATION.md. Earlier validation claims are not evidence for this release.

# Execucao local dos scripts de teste

Os scripts sao legiveis/editaveis. Leia antes de executa-los.

**Terminal 1 — Go**

```powershell
cd "CAMINHO\jungle-backend-challenge-stage4-revisada"
Copy-Item .env.example .env  # somente a primeira vez
notepad .env                 # colocar a senha local do PostgreSQL Docker
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
.\scripts\start-stage4.ps1
```

**Terminal 2 — validacao**

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
.\scripts\test-stage4.ps1
.\scripts\check-schema.ps1
```

O `start-stage4.ps1` exige porta 5433 e escuta na interface local; nao confunde com PostgreSQL 18 do Windows (5432). O teste escreve novos registros, mas nao altera os antigos. O `.env` nao deve ser commitado; ele nao vem no ZIP.

## Etapa 7

- `setup-sqs.ps1`: provisiona comandos/events/DLQ FIFO e chaves HMAC individuais no `.env` local (não versionar).
- `start-stage7.ps1`: carrega `.env`, valida PostgreSQL Docker 5433 + Keycloak localhost:8081 + LocalStack 4566 e inicia API/consumidor/publisher.
- `test-stage7.ps1`: carteira interna, BET/WIN por SQS com HMAC, SQS redelivery sem duplicação, ledger e outbox publicado.
- `test-stage7-db.ps1`: `go test -race` com banco real, incluindo rollback transacional de inbox, idempotência e concorrência.
- `check-stage7.ps1`: histórico 001–005, snapshot inbox/outbox e reconciliação.
- `test-stage7-recovery.ps1`: pausa/reabilita LocalStack para provar commit financeiro antes da publicação e recuperação da outbox.

## Etapa 8 — validação com três processos reais

Consulte `docs/ETAPA8_TESTES_DISTRIBUIDOS.md`. Sequência na pasta da Etapa 8, **com a API antiga encerrada e o `.env` da Etapa 7 copiado**:

```powershell
.\scripts\start-stage8.ps1
.\scripts\test-stage8.ps1
.\scripts\test-stage8-failover.ps1
.\scripts\test-stage8-db.ps1
.\scripts\check-stage8.ps1
.\scripts\stop-stage8.ps1
```

Os testes usam novos IDs e preservam todas as carteiras, operações e migrações existentes. Relatórios gerados em `.stage8-evidence/`.
