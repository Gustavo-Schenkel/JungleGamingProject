> Historical stage reference. For the supported delivery workflow and current results, read README.md and docs/VALIDATION.md. Earlier validation claims are not evidence for this release.

# Etapa 7 — Amazon SQS FIFO, Inbox, Outbox e recuperação

## Decisões de arquitetura

O conteúdo deste projeto parte do ZIP real enviado da **Etapa 6**. As migrações `001` a `004` permanecem intactas; `005_stage7_messaging.sql` é estritamente aditiva. Nenhuma tabela financeira é apagada ou substituída.

### Fluxos

**HTTP autorizado:** Keycloak → `POST /v1/transactions` → `finance.Service.Process` → transação PostgreSQL com `wager_transactions` + `wallets` + `ledger_entries` + **`sqs_outbox`**. Após o COMMIT, um publisher independente publica o evento na FIFO de saída. O evento nunca é enviado antes do commit.

**Mensagem financeira autorizada:** FIFO de entrada `jungle-commands.fifo` → validação HMAC-SHA256 por chave de cliente/provedor → transação SQL incluindo **`sqs_inbox` + mesma rotina `finance.Service.ProcessInTx` + wallet + ledger + outbox** → COMMIT → DeleteMessage. Se o processo cair antes do commit, todos os efeitos são revertidos e a mensagem volta após o timeout de visibilidade. Se o processo cair entre commit e DeleteMessage, o marcador persistente da inbox reconhece a mensagem e evita novo processamento; se outra mensagem física repetir a mesma operação, a idempotência financeira persistente evita duplicação.

O `providerId` é obtido do mapeamento de `clientId` autorizado no servidor; o JSON fornecido pelo produtor não define a identidade. Para mensagens SQS, a autenticidade é verificada por HMAC **com segredo próprio e diferente do client secret do Keycloak**; em AWS real, configure adicionalmente IAM de menor privilégio, criptografia, TLS e políticas de acesso às filas. O endpoint HTTP permanece protegido por JWT/OIDC.

### Publisher e retomada

- `sqs_outbox`: estados `PENDING`, `IN_FLIGHT` (lease de 45 segundos) e `PUBLISHED`; retries com backoff até 256 segundos; `FOR UPDATE SKIP LOCKED` e `lease_token` impedem duas instâncias de marcar um mesmo lease indevidamente. Eventos de uma mesma carteira são publicados em ordem, aguardando o evento anterior.
- Mensagens de saída usam `MessageGroupId=walletId` e `MessageDeduplicationId=outboxEventId`. Se ocorrer falha depois do envio e antes do ACK no banco, a mensagem pode ser reenviada: consumidores externos devem deduplicar por `eventId`. A janela de deduplicação da FIFO sozinha **não** constitui garantia de exactly-once ponta a ponta.
- `sqs_inbox` usa `(consumer_name,message_id)` como chave única. Comandos de negócio repetidos sob outro MessageId continuam protegidos por `wager_transactions(provider_id,idempotency_key)` e `(provider_id,external_transaction_id)`.
- Mensagens de entrada inválidas ou indisponibilidade do banco **não** são confirmadas. A fila de entrada tem `VisibilityTimeout=30`, `maxReceiveCount=4` e DLQ `jungle-commands-dlq.fifo`.
- `LOSS` mantém saldo e versão; `PENDING_REFERENCE` gera evento e a transição posterior gera outro evento somente após o commit do worker existente da Etapa 5.

## Configuração e execução no Windows

**Antes de tudo:** seu contêiner PostgreSQL deve estar em `127.0.0.1:5433`, Keycloak em `localhost:8081` e LocalStack em `127.0.0.1:4566`. Não rode `docker compose down -v`. O projeto pode usar o stack Docker já mantido pela pasta da Etapa 2; **não** recrie os serviços em outra pasta se já estiverem ativos.

1. Extraia `jungle-backend-challenge-stage7-messaging.zip`; copie **seu `.env` da Etapa 6** para a nova pasta. Não use um `.env` de exemplo como se já contivesse credenciais reais.
2. Execute `Get-ChildItem .\scripts -Filter *.ps1 | Unblock-File` somente após conferir os scripts baixados. Sua política permanente de usuário `RemoteSigned` é suficiente.
3. Pare a aplicação Go anterior com `Ctrl+C` (a porta 8080 é reutilizada).
4. Execute `go mod tidy`. Esta etapa acrescenta a dependência oficial **AWS SDK for Go v2**; o primeiro download de módulos exige internet.
5. Execute `.\scripts\setup-sqs.ps1`. O script acrescenta configurações `SQS_*` ao `.env`, gera dois segredos HMAC fortes localmente e provisiona filas sem apagar as existentes. **Nunca compartilhe o `.env`.**
6. Execute `$env:Path="C:\msys64\ucrt64\bin;$env:Path"; $env:CGO_ENABLED="1"; $env:CC="gcc"` e depois `go test -race -count=1 ./...`.
7. Execute `.\scripts\start-stage7.ps1` e aguarde `[Fx] RUNNING`.
8. Em outro PowerShell na mesma pasta, execute `.\scripts\test-stage7.ps1`.
9. Execute `.\scripts\test-stage7-db.ps1`, `.\scripts\check-stage7.ps1` e, por último, `.\scripts\test-stage7-recovery.ps1` (faz `docker pause` temporário no LocalStack; o `finally` do script restaura o serviço).

O consumidor SQS **não** aceita comandos com apenas um nome de provedor declarado em texto. O produtor de teste `go run ./cmd/sqs-send ...` assina o corpo com um segredo local; a assinatura é verificada pelo consumidor, que deriva a identidade do allowlist.

### Filas e comandos manuais

| Nome | Função |
|---|---|
| `jungle-commands.fifo` | Comandos financeiros assinados que chegam ao consumidor |
| `jungle-events.fifo` | Eventos financeiros persistidos no outbox e publicados após COMMIT |
| `jungle-commands-dlq.fifo` | Mensagens recebidas repetidamente sem êxito; investigar e reprocessar com controle |

- `GET /health/ready`: conectividade PostgreSQL (não substitui o teste do SQS).
- `GET /internal/dev/messaging/status`: requer o token do cliente interno + modo `development` + acesso loopback. Retorna contagem de eventos PENDING, IN_FLIGHT, PUBLISHED e mensagens confirmadas na inbox.
- `SELECT * FROM stage7_messaging_status;`: verificações no pgAdmin ou `docker exec`.
- `SELECT * FROM stage4_wallet_audit WHERE balance_minor_units <> ledger_net_minor_units;`: resultado esperado **zero linhas**.

### Limites / próximos critérios de aceite

Esta implementação fornece persistência de efeitos financeiros e entrega **at least once**, não exactly once universal. A DLQ está configurada, mas o teste automático principal cobre sucesso, duplicação e recuperação do publisher; validação explícita de mensagens inválidas chegando à DLQ e reprocessamento supervisionado deve ser incluída na bateria final. **A Etapa 8 precisará iniciar pelo menos três processos independentes simultâneos**, forçar falhas entre commit/envio/ack e validar idempotência e saldo sob concorrência distribuída. A validação SQS real ainda deve ser feita no computador com Docker e Go; o ZIP não representa testes integrados já aprovados.
