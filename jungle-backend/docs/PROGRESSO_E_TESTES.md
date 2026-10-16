> Historical stage reference. For the supported delivery workflow and current results, read README.md and docs/VALIDATION.md. Earlier validation claims are not evidence for this release.

# Registro de progresso e testes — Jungle Gaming

**Data de atualização:** 15/10/2026. Os resultados PASS abaixo provêm de saídas e capturas enviadas pelo usuário no Windows, não de alegações de testes executados no ambiente de empacotamento.

| Etapa | Escopo | Evidência confirmada | Status |
|---|---|---|---|
| 1–3 | Go/Fx, migrations, PostgreSQL Docker, carteiras | API `ready`, carteira `jogador001` saldo 0/versão 1 persistida | PASS no Windows |
| 4 | OPENING, BET, WIN, ledger, idempotência | script `STAGE 4 PASSED`, ledger exato em HTTP+SQL, zero divergências; Go `-race` passou | PASS no Windows |
| 5 | LOSS, REFUND, ROLLBACK, pending reference e worker | `STAGE 5 PASSED`, teste DB/race `internal/finance ok`, migration 004, zero divergências | PASS no Windows |
| 6 | Keycloak OAuth2/OIDC, isolamento de provedores | `STAGE 6 PASSED`, Keycloak 3 tokens, 401 inválido, 403/404 acesso indevido, beta não lê alpha, Go `-race` passou | PASS no Windows |
| 7 | SQS FIFO, inbox/outbox, replay, recuperação | `STAGE 7 PASSED`, DB/race corrigido (chave fixa do teste), outage recovery passou; `check-stage7.ps1` 001–005 e reconciliação com 0 diferenças | PASS nos cenários executados |
| 8 | Três processos Go, double spend entre processos, idempotência concorrente, sobrevivência de dois processos e workers SQS | scripts e verificador Go incluídos neste pacote; **não executados ainda no Windows** | PENDENTE |

## Auditoria anterior à Etapa 8

Última captura recebida de `check-stage7.ps1`:

- PostgreSQL 16 no Docker, `127.0.0.1:5433 → 5432`, saudável.
- Migrations: `001_initial_schema.sql`, `002_stage4_financial_operations.sql`, `003_stage4_audit_guards.sql`, `004_stage5_reversals.sql`, `005_stage7_messaging.sql` aplicadas.
- `sqs_outbox`: **66 PUBLISHED**, **0 PENDING**, **0 IN_FLIGHT**; `sqs_inbox`: **7 processadas**.
- Carteiras com saldo divergente do ledger: **0**.
- Recovery de LocalStack: commit financeiro BRL 80.00 persistiu durante indisponibilidade; Outbox publicou após retorno (`STAGE 7 OUTAGE RECOVERY PASSED`).

Esses contadores são o snapshot anterior aos testes da Etapa 8; novas execuções acrescentam eventos e não devem preservar os mesmos totais absolutos.

## Etapa 8 — checklist de entrega

- [ ] `start-stage8.ps1`: três PIDs e três portas prontos
- [ ] `test-stage8.ps1`: disputa R$ 100 / R$ 80 + R$ 80 com 1 aprovado, 1 insuficiente, saldo R$ 20, ledger sem duplicações
- [ ] `test-stage8.ps1`: 30 requisições com uma mesma chave produzem um único débito e a mesma resposta
- [ ] `test-stage8.ps1`: SQS com reentrega, inbox duplicada, outbox publicado uma vez por decisão
- [ ] `test-stage8-failover.ps1`: instância 8082 encerrada, 8080+8083 continuam, 8082 restaurada
- [ ] `test-stage8-db.ps1`: suite `go test -race -count=1 ./...` com integração PostgreSQL e SQS
- [ ] `check-stage8.ps1`: migrations 001–005 e nenhuma divergência de ledger
- [ ] Confirmar que scripts e logs não contêm senhas/tokens em texto puro antes de compartilhar a entrega

Relatórios locais esperados: `.stage8-evidence/stage8-full.json` e `.stage8-evidence/stage8-failover.json`. Incluir na documentação final somente após aprovação no Windows.

## Limitações remanescentes

- Múltiplos workers compartilhando SQS garantem coordenação nos cenários avaliados, não distribuição uniforme de mensagens.
- A simulação de failover interrompe um processo entre operações; testes determinísticos de kill **entre cada instrução SQL** ou **entre ack SQS e commit** não estão incluídos.
- Não foi implementado monitoramento Prometheus/OpenTelemetry, TLS de produção, métricas DLQ nem testes de carga contínua. Não descrever o projeto como produção-ready sem estes complementos.
- Os scripts da Etapa 8 dependem de PowerShell/Windows e de credenciais locais no `.env`. Nenhum segredo deverá entrar no repositório Git.
