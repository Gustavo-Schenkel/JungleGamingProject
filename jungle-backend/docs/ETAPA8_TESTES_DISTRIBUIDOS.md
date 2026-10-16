> Historical stage reference. For the supported delivery workflow and current results, read README.md and docs/VALIDATION.md. Earlier validation claims are not evidence for this release.

# Etapa 8 — Testes distribuídos, failover e evidências

**Projeto:** Desafio Backend — Processamento Distribuído de Apostas em Go (Jungle Gaming)  
**Estado deste pacote:** implementado, **aguardando execução e validação no Windows do usuário**. Não presumir que passou sem as saídas do terminal.

## Objetivo e critérios de aceitação

A Etapa 8 **não altera** a lógica financeira, autenticação, SQS, banco de dados ou migrações das Etapas 1–7. Ela demonstra que a solução existente é correta com **três processos independentes da API** compartilhando apenas PostgreSQL, Keycloak e SQS LocalStack.

| Cenário | Critério auditável |
|---|---|
| Três instâncias | três PIDs independentes, três portas distintas: `8080`, `8082`, `8083`, todos `ready` |
| Concorrência real | carteira R$ 100; duas apostas distintas de R$ 80 simultâneas, enviadas para **processos diferentes**; exatamente uma `PROCESSED`, outra `REJECTED/INSUFFICIENT_FUNDS` |
| Saldo e versão | saldo R$ 20,00 (`2000` centavos), versão `2` e exatamente dois lançamentos (`OPENING` e um único débito `BET`) |
| Idempotência entre instâncias | repetir cada requisição em outro processo retorna o mesmo HTTP status, transactionId, resultado e versão |
| Disputa da mesma chave | 30 requisições simultâneas distribuídas pelas três instâncias, mesma chave e payload; um transactionId, um débito, saldo R$ 75, versão 2 |
| SQS com múltiplos workers | comando BET recebido por qualquer worker; reentrega como novo messageId não gera segundo débito; inbox registra as mensagens; outbox tem dois eventos da carteira (OPENING e BET), ambos `PUBLISHED` |
| Reinicialização de processo | encerrar apenas a instância `8082` (por PID verificado), executar os mesmos testes com `8080` + `8083`, e restaurar a terceira instância |
| Reconciliação | `stage4_wallet_audit` não apresenta diferenças em nenhuma carteira |
| Corridas | `go test -race -count=1 ./...`, com testes que acessam PostgreSQL real (`RUN_DB_INTEGRATION=1`) |

**Limitação:** testes de race avaliam corridas de dados detectáveis pelo runtime Go durante os testes executados. Os cenários acima comprovam integridade nos casos medidos; não demonstram formalmente ausência de qualquer falha possível, nem garantem distribuição igual das mensagens SQS entre os workers.

## Pré-requisitos

- Windows PowerShell 5.1 ou 7, Go 1.23+ e GCC/MSYS2 instalado (para o detector `-race`).
- Docker Desktop ativo com contêineres `jungle-postgres`, `jungle-localstack` e `jungle-keycloak` (mesma instalação validada na Etapa 7).
- PostgreSQL do Docker disponível em `127.0.0.1:5433` (não confundir com PostgreSQL 18 do Windows em 5432).
- Keycloak em `http://localhost:8081`, realm `jungle` e clientes já configurados.
- LocalStack em `http://127.0.0.1:4566`; filas criadas pela Etapa 7.
- Cópia local do **`.env` da Etapa 7**, contendo configuração do PostgreSQL, OIDC e SQS, inclusive segredos de clientes e assinaturas HMAC. **Nunca incluir o `.env` na entrega Git/ZIP.**
- **Encerrar a API anterior** (`Ctrl+C` no terminal Stage7) antes de iniciar as três instâncias; a porta 8080 precisa estar disponível.

## Passo a passo no PowerShell

Na pasta `jungle-backend-challenge-stage8-distributed`:

```powershell
# Execute somente se o ZIP vier bloqueado pela marca de download:
Get-ChildItem .\scripts -Filter '*.ps1' -Recurse | Unblock-File

# 1. Conferir componentes antes de iniciar a API distribuída:
docker ps

# 2. Iniciar as TRÊS instâncias (um binário, três processos/pids):
.\scripts\start-stage8.ps1

# 3. Teste ponta-a-ponta com três processos e SQS compartilhado:
.\scripts\test-stage8.ps1

# 4. Derrubar somente a instância da porta 8082,
#    testar continuidade com 8080+8083 e reiniciar 8082:
.\scripts\test-stage8-failover.ps1

# 5. Testes Go sem cache + -race + integração PostgreSQL:
.\scripts\test-stage8-db.ps1

# 6. Verificar PIDs, migrações, status da mensageria e reconciliação:
.\scripts\check-stage8.ps1

# 7. Somente depois dos testes, encerrar os 3 processos gerenciados:
.\scripts\stop-stage8.ps1
```

Caso `-race` informe CGO desativado, o script `test-stage8-db.ps1` define `CGO_ENABLED=1`, `CC=gcc` e adiciona `C:\msys64\ucrt64\bin` ao PATH **na sessão de execução**.

### Controle de processos

`start-stage8.ps1` compila `.stage8-bin/jungle-api-stage8.exe` e inicia processos separados que escutam exclusivamente em `127.0.0.1:8080`, `127.0.0.1:8082` e `127.0.0.1:8083`. Cada processo executa seus próprios workers Uber Fx; a coordenação ocorre no PostgreSQL e no SQS. O executável compartilhado é compilado **apenas quando nenhuma instância existente o estiver utilizando**, pois o Windows bloqueia sobrescrever um EXE ativo.

Os PIDs e horários de criação são registrados em `.stage8-run/instances.json`. O script de parada **só encerra** processos cujo caminho executável e horário de início correspondam a esse registro. Não força a parada de serviços Docker ou outros processos Go.

Para parar uma única instância manualmente:

```powershell
.\scripts\stop-stage8.ps1 -Ports 8082
.\scripts\start-stage8.ps1 -Ports 8082
```

Se um programa externo já utilizar uma das três portas, a inicialização interrompe com uma mensagem; ela **não derruba** o processo externo. Se um processo caiu inesperadamente e o estado registrado ficou obsoleto, inspecione os logs `.stage8-run/api-*.stderr.log` e os PIDs antes de fazer qualquer ajuste manual.

### Evidências produzidas

- `.stage8-evidence/stage8-full.json` — resultados e carteiras da execução com três instâncias.
- `.stage8-evidence/stage8-failover.json` — execução com apenas dois processos sobreviventes.
- `.stage8-run/instances.json` — PIDs e horários (operacional, não é evidência permanente).
- `.stage8-run/api-*.stderr.log` — logs de cada processo para investigação, **não enviar indiscriminadamente** sem revisar conteúdo.

Os relatórios JSON não contêm tokens JWT, senhas ou segredos HMAC. Eles incluem os identificadores das carteiras geradas, permitindo auditá-las posteriormente no PostgreSQL. Todos os cenários usam identificadores novos para evitar colisão com históricos persistidos. **Os testes inserem novas carteiras e eventos de auditoria, mas nunca excluem os existentes.**

## Consulta manual independente do relatório

No pgAdmin 4 conectado à porta **5433** ou via `docker exec`:

```sql
SELECT version, applied_at FROM schema_migrations ORDER BY version;
SELECT * FROM stage7_messaging_status;
SELECT wallet_id, player_id, balance_minor_units, ledger_net_minor_units
FROM stage4_wallet_audit
WHERE balance_minor_units <> ledger_net_minor_units;
```

Última consulta deve retornar **zero linhas**. Migrações esperadas: 001–005; **não há migração 006**, pois a Etapa 8 é dedicada à validação da implementação já existente.

## Análise de falhas

- **Porta 8080 ocupada:** encerre a API da Etapa 7 com `Ctrl+C`, sem parar os contêineres.
- **PowerShell bloqueia script:** revise os `.ps1` e execute `Get-ChildItem .\scripts -Filter '*.ps1' -Recurse | Unblock-File` após confiar na origem; não é necessário `Bypass` permanente.
- **Faltam segredos Keycloak/SQS:** copie o `.env` da Etapa 7; não execute setup novamente se já possui segredos válidos.
- **Erro PostgreSQL 42703, esquema vazio ou conexão recusada:** confirme porta **5433**, histórico de migrações 001–005 e conta configurada; não recrie tabelas nem remova volumes.
- **Race test `CGO_ENABLED` ou gcc:** instale/ajuste o MSYS2 UCRT64 e execute `test-stage8-db.ps1`.
- **Instância falha na inicialização:** consulte `.stage8-run/api-PORT.stderr.log` e o processo gerenciado em `.stage8-run/instances.json`.
- **SQS demorou para publicar:** teste aguarda até 70 s por Inbox/Outbox; verifique LocalStack `healthy`, suas filas FIFO, o clock e logs dos workers.
- **Simulação de failover falha:** o script tenta restaurar a porta 8082 em `finally`; use `start-stage8.ps1 -Ports 8082` somente se ela permanecer sem processo registrado.

## Fora do escopo comprovado desta etapa

Nem `test-stage8.ps1` nem `-race` provam recuperação de toda janela de falha entre commit/ACK, eliminação universal de duplicações em condições arbitrárias ou tolerância a partições duradouras. Os testes anteriores da Etapa 7 avaliaram indisponibilidade temporária do LocalStack, retry de outbox e atomicidade da inbox; a Etapa 8 verifica **concorrência e sobrevivência real de processos**. Para produção, ainda são desejáveis métricas Prometheus/OpenTelemetry, segredos em secret manager, TLS, deploy automatizado, monitoramento de DLQ e testes de carga de longo prazo.
