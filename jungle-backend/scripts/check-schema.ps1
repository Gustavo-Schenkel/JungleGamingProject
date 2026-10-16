$ErrorActionPreference = 'Stop'
Write-Host 'PostgreSQL Docker:' -ForegroundColor Cyan
docker ps --filter 'name=jungle-postgres' --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
Write-Host 'Migracoes aplicadas:' -ForegroundColor Cyan
docker exec jungle-postgres psql -U postgres -d jungle -c 'SELECT version, applied_at FROM schema_migrations ORDER BY version;'
Write-Host 'Colunas financeiras de wallets:' -ForegroundColor Cyan
docker exec jungle-postgres psql -U postgres -d jungle -c "SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name='wallets' ORDER BY ordinal_position;"
Write-Host 'Conferencia de saldo por ledger (somente divergencias):' -ForegroundColor Cyan
docker exec jungle-postgres psql -U postgres -d jungle -c 'SELECT wallet_id, player_id, balance_minor_units, ledger_net_minor_units, ledger_entry_count FROM stage4_wallet_audit WHERE balance_minor_units <> ledger_net_minor_units ORDER BY wallet_id;'
