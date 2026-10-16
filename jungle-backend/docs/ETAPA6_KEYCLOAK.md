> Historical stage reference. For the supported delivery workflow and current results, read README.md and docs/VALIDATION.md. Earlier validation claims are not evidence for this release.

# Etapa 6 — Autenticação OAuth2/OIDC com Keycloak 26

Esta etapa substitui o acesso financeiro sem autenticação da Etapa 5 por **tokens de acesso JWT RS256** fornecidos por Keycloak. Não cria uma nova migração: as migrações **001–004 continuam** e seus dados anteriores não são apagados.

## Requisitos

- PostgreSQL 16 do Docker: `127.0.0.1:5433`, banco `jungle` (mantenha seus dados).
- Keycloak 26 do Docker: `http://localhost:8081` (`jungle-keycloak`).
- Go 1.23 ou superior, PowerShell 5.1+.
- Pare a API da Etapa 5 (porta HTTP 8080) antes de iniciar a Etapa 6.
- Scripts `.ps1` devem ser revisados antes da execução. Com `RemoteSigned`, se arquivos extraídos do ZIP estiverem bloqueados, desbloqueie apenas os scripts revisados: `Get-ChildItem .\scripts -Filter *.ps1 | Unblock-File`.

## Preparação

Abra a pasta `jungle-backend-challenge-stage6-oidc` no VS Code. Copie o `.env` da Etapa 5 (ele já aponta para o Docker `5433`), **ou** copie `.env.example` para `.env` e preencha a senha local PostgreSQL e a senha de administrador Keycloak. Em ambos os casos, confira que as variáveis a seguir estão no `.env`:

```dotenv
APP_ADDR=127.0.0.1:8080
APP_ENV=development
POSTGRES_HOST=127.0.0.1
POSTGRES_PORT=5433
POSTGRES_DB=jungle
POSTGRES_USER=postgres
POSTGRES_PASSWORD=<SENHA_LOCAL>
POSTGRES_SSLMODE=disable
OIDC_ISSUER=http://localhost:8081/realms/jungle
OIDC_AUDIENCE=jungle-api
OIDC_PROVIDER_CLIENTS=jungle-provider-alpha:provider-alpha,jungle-provider-beta:provider-beta
OIDC_INTERNAL_CLIENT=jungle-internal
KEYCLOAK_ADMIN=admin
KEYCLOAK_ADMIN_PASSWORD=<SENHA_ADMIN_ATUAL_KEYCLOAK>
```

**Nunca envie ou faça commit do `.env`**. Scripts de provisionamento geram credenciais de clientes e as acrescentam **somente ao seu `.env`**.

1. Confira com `docker ps` que `jungle-keycloak` e `jungle-postgres` estão ativos. Os contêineres existentes podem ser iniciados no Docker Desktop; **não use** `docker compose down -v`.
2. Na pasta da Etapa 6, execute `.\scripts\setup-keycloak.ps1`. Ele cria (ou atualiza) o realm `jungle` e três **confidential clients** com `client_credentials`, adicionando audiência `jungle-api` aos access tokens. A configuração é idempotente para realm e clients existentes. Não apaga usuários, dados ou banco.
3. Execute `go test -count=1 ./...` (com GCC/CGO funcionando, também `go test -race -count=1 ./...`).
4. Execute `.\scripts\start-stage6.ps1` e mantenha esse terminal aberto. Se o Keycloak estiver indisponível ou o OIDC estiver mal configurado, o aplicativo falhará na inicialização, em vez de permitir acesso sem autenticação.
5. Em **outro PowerShell** na mesma pasta, execute `.\scripts\test-stage6.ps1`. O script testa Keycloak e endpoints protegidos com três tokens emitidos pelo realm, incluindo tentativas de acesso proibidas e uma transação financeira real persistida no banco.
6. Opcionalmente execute `.\scripts\check-schema.ps1` para verificar novamente as migrações e a reconciliação do ledger.

> Atenção: `setup-keycloak.ps1` depende da senha de administrador REAL do container Keycloak já inicializado. Alterar `KEYCLOAK_ADMIN_PASSWORD` no `.env` não atualiza automaticamente as credenciais de um administrador existente. Se o login falhar, verifique a senha atual do Keycloak, sem recriar o banco financeiro.

## Identidades de máquina (M2M)

| Client ID Keycloak | Provedor autorizado na API | Permissão |
| --- | --- | --- |
| `jungle-provider-alpha` | `provider-alpha` | POST/GET `/v1/transactions` (somente escopo alpha) |
| `jungle-provider-beta` | `provider-beta` | POST/GET `/v1/transactions` (somente escopo beta) |
| `jungle-internal` | não tem `providerId` | endpoints `/internal/...` de carteira/ledger, em desenvolvimento e localhost |

O `providerId` é determinado no servidor a partir do `azp` de um token **assinado e validado**. Caso o cliente informe `providerId` no corpo, o servidor exige que corresponda ao token; caso contrário devolve **403**. O identificador externo de transação e a chave de idempotência são persistidos por provedor, conforme as migrações existentes. Consultas de transações por outro provedor retornam **404**.

Um `Bearer` JWT é aceito apenas se passar por todos os controles: assinatura RS256 válida (Keycloak JWKS), `kid` permitido, issuer e audience exatos, `typ=Bearer`, expiração, `nbf`, `iat`, `sub` e `azp` na lista configurada. A chave pública é atualizada via descoberta OIDC. Não há modo anônimo de desenvolvimento.

## Endpoints

| Verbo | Rota | Quem pode usar |
| --- | --- | --- |
| `GET` | `/health/live`, `/health/ready` | Público (health checks, sem dados financeiros) |
| `POST` | `/v1/transactions` | Provider alpha/beta, token Bearer, header `Idempotency-Key` |
| `GET` | `/v1/transactions/{externalTransactionId}` | Provider alpha/beta, escopo determinado pelo token |
| `POST` | `/internal/dev/wallets/open` | Client `jungle-internal`, somente desenvolvimento/loopback |
| `POST` | `/internal/wallets` | Client `jungle-internal`, somente desenvolvimento/loopback |
| `GET` | `/internal/wallets/{id}` | Client `jungle-internal`, somente desenvolvimento/loopback |
| `GET` | `/internal/dev/wallets/{id}/ledger` | Client `jungle-internal`, somente desenvolvimento/loopback |

**A rota legada** `POST /internal/dev/wagering/transactions` **não está mais registrada**: operações de provedores devem usar `/v1/transactions` com JWT verificado. Dessa forma o script de testes da Etapa 5 (sem autenticação) **não deve ser executado contra esta API**; seus resultados anteriores e o código financeiro permanecem preservados. Use `test-stage6.ps1` para conferir o novo contrato de segurança.

Corpo do POST `/v1/transactions` (exemplo, substitua identificadores pelo ambiente local):

```json
{
 "externalTransactionId": "bet-123", "playerId": "player-1", "walletId": "UUID_DA_CARTEIRA",
 "roundId": "round-1", "gameId": "game-1", "kind": "BET",
 "money": {"amount": "10.00", "currency": "BRL"}
}
```

A carteira deve ser criada pelo cliente **interno autenticado**. `OPENING` é uma operação interna de desenvolvimento, não um mecanismo que os provedores podem invocar. Dados financeiros das Etapas 1–5 permanecem na mesma base.

## Critérios para marcar a Etapa 6 como aprovada

- `go test -race -count=1 ./...` sem `FAIL`.
- `setup-keycloak.ps1` cria realm e clients sem alterar PostgreSQL.
- `start-stage6.ps1` exibe `[Fx] RUNNING`.
- `test-stage6.ps1` termina com `STAGE 6 PASSED`, incluindo 401 sem token, 401 token adulterado, 403 spoof do provider e 404 de transação alheia.
- `check-schema.ps1` continua sem divergências de saldo contra ledger.

## Limitações explícitas

- O Keycloak foi configurado para desenvolvimento em HTTP sobre **loopback**. Para produção, HTTPS, rotação/gestão segura de segredos, configuração de rede, logs de auditoria, rate limiting e política de usuários precisam de revisão.
- Este projeto valida JWT assinado e expiração localmente. **Revogação instantânea** de um JWT já emitido depende da expiração curta ou de mecanismo adicional (não implementado).
- A permissão de realizar operações em carteira compartilhada é determinada pelo serviço de apostas e identificadores de jogador/carteira, não por uma associação exclusiva carteira→provedor. Um provedor autenticado não recebe acesso de leitura a carteiras internas nem pode consultar transações de outro provedor.
- SQS, inbox/outbox e o teste de **três processos independentes** continuam pendentes para as próximas etapas.
