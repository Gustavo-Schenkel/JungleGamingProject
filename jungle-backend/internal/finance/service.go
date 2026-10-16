// Package finance owns transactional financial operations in Stages 4 and 5.
// All persisted decisions are committed atomically through PostgreSQL.
package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid             = errors.New("invalid financial operation")
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrWalletMismatch      = errors.New("wallet/player/currency mismatch")
	ErrConflict            = errors.New("idempotency key or external transaction ID conflict")
	ErrTransactionNotFound = errors.New("transaction not found")
)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Command fields are external business fields. The HTTP Idempotency-Key is separate.
type Command struct {
	ProviderID                     string          `json:"providerId"`
	ExternalTransactionID          string          `json:"externalTransactionId"`
	PlayerID                       string          `json:"playerId"`
	WalletID                       string          `json:"walletId"`
	RoundID                        string          `json:"roundId"`
	GameID                         string          `json:"gameId"`
	Kind                           string          `json:"kind"`
	Money                          money.JSONMoney `json:"money"`
	ReferenceExternalTransactionID string          `json:"referenceExternalTransactionId,omitempty"`
}

type Result struct {
	TransactionID string          `json:"transactionId"`
	Status        string          `json:"status"`
	Balance       money.JSONMoney `json:"balance"`
	WalletVersion int64           `json:"walletVersion"`
	FailureCode   string          `json:"failureCode,omitempty"`
}

type OpeningCommand struct {
	PlayerID       string          `json:"playerId"`
	InitialBalance money.JSONMoney `json:"initialBalance"`
}

type LedgerEntry struct {
	ID            string          `json:"id"`
	WalletID      string          `json:"walletId"`
	TransactionID string          `json:"transactionId"`
	EntryType     string          `json:"entryType"`
	Direction     string          `json:"direction"`
	Amount        money.JSONMoney `json:"amount"`
	BalanceBefore money.JSONMoney `json:"balanceBefore"`
	BalanceAfter  money.JSONMoney `json:"balanceAfter"`
}

type Service struct{ DB *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{DB: db} }

func validField(s string) bool {
	return s != "" && len(s) <= 200 && strings.TrimSpace(s) == s && !strings.ContainsRune(s, 0)
}

// canonicalHash uses deterministic field order (struct JSON), exact minor units,
// and every external business field; it excludes the idempotency key/transport.
func canonicalHash(c Command, m money.Money) (string, error) {
	canon := struct {
		ProviderID                     string `json:"providerId"`
		ExternalTransactionID          string `json:"externalTransactionId"`
		PlayerID                       string `json:"playerId"`
		WalletID                       string `json:"walletId"`
		RoundID                        string `json:"roundId"`
		GameID                         string `json:"gameId"`
		Kind                           string `json:"kind"`
		Currency                       string `json:"currency"`
		AmountMinorUnits               int64  `json:"amountMinorUnits"`
		ReferenceExternalTransactionID string `json:"referenceExternalTransactionId"`
	}{c.ProviderID, c.ExternalTransactionID, c.PlayerID, c.WalletID, c.RoundID, c.GameID, c.Kind, m.Currency(), m.MinorUnits(), c.ReferenceExternalTransactionID}
	data, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func validate(c Command, key string) (money.Money, string, error) {
	if !validField(key) || !validField(c.ProviderID) || !validField(c.ExternalTransactionID) ||
		!validField(c.PlayerID) || !validField(c.RoundID) || !validField(c.GameID) || !uuidPattern.MatchString(c.WalletID) {
		return money.Money{}, "", ErrInvalid
	}
	switch c.Kind {
	case "BET", "WIN", "LOSS", "REFUND", "ROLLBACK":
	default:
		return money.Money{}, "", ErrInvalid
	}
	if c.Kind == "REFUND" || c.Kind == "ROLLBACK" {
		if !validField(c.ReferenceExternalTransactionID) || c.ReferenceExternalTransactionID == c.ExternalTransactionID {
			return money.Money{}, "", ErrInvalid
		}
	} else if c.ReferenceExternalTransactionID != "" {
		if c.Kind != "WIN" || !validField(c.ReferenceExternalTransactionID) ||
			c.ReferenceExternalTransactionID == c.ExternalTransactionID {
			return money.Money{}, "", ErrInvalid
		}
	}
	amount, err := c.Money.Parse()
	if err != nil || (c.Kind == "LOSS" && amount.MinorUnits() != 0) ||
		(c.Kind != "LOSS" && amount.MinorUnits() <= 0) {
		return money.Money{}, "", ErrInvalid
	}
	h, err := canonicalHash(c, amount)
	return amount, h, err
}

func isUniqueViolation(err error) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == "23505"
}

// OpenWallet creates a fresh wallet and, when positive, its OPENING and ledger
// in the SAME commit. Opening does not increase the creation-time version (1).
func (s *Service) OpenWallet(ctx context.Context, cmd OpeningCommand) (wallet.Wallet, error) {
	if !validField(cmd.PlayerID) {
		return wallet.Wallet{}, ErrInvalid
	}
	amount, err := cmd.InitialBalance.Parse()
	if err != nil {
		return wallet.Wallet{}, ErrInvalid
	}
	wid, err := wallet.NewID()
	if err != nil {
		return wallet.Wallet{}, err
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return wallet.Wallet{}, fmt.Errorf("begin opening: %w", err)
	}
	defer tx.Rollback(ctx)
	var w wallet.Wallet
	err = tx.QueryRow(ctx, `INSERT INTO wallets(id,player_id,currency,balance_minor_units)
  VALUES ($1,$2,$3,$4) RETURNING id,player_id,currency,balance_minor_units,version`, wid, cmd.PlayerID, amount.Currency(), amount.MinorUnits()).Scan(
		&w.ID, &w.PlayerID, &w.Currency, &w.BalanceMinorUnits, &w.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return wallet.Wallet{}, wallet.ErrExists
		}
		return wallet.Wallet{}, fmt.Errorf("create wallet: %w", err)
	}
	if amount.MinorUnits() > 0 {
		oid, err := wallet.NewID()
		if err != nil {
			return wallet.Wallet{}, err
		}
		lid, err := wallet.NewID()
		if err != nil {
			return wallet.Wallet{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO opening_transactions (id,wallet_id,player_id,currency,amount_minor_units)
   VALUES($1,$2,$3,$4,$5)`, oid, w.ID, w.PlayerID, w.Currency, amount.MinorUnits())
		if err != nil {
			return wallet.Wallet{}, fmt.Errorf("insert opening: %w", err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger_entries
   (id,wallet_id,transaction_id,entry_type,direction,amount_minor_units,currency,balance_before_minor_units,balance_after_minor_units)
   VALUES($1,$2,$3,'OPENING','CREDIT',$4,$5,0,$4)`, lid, w.ID, oid, amount.MinorUnits(), amount.Currency())
		if err != nil {
			return wallet.Wallet{}, fmt.Errorf("insert opening ledger: %w", err)
		}
	}
	if err = enqueueEvent(ctx, tx, w.ID, "", "", "", "OPENING", "PROCESSED", w.Currency, w.BalanceMinorUnits, w.Version); err != nil {
		return wallet.Wallet{}, fmt.Errorf("append opening outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return wallet.Wallet{}, fmt.Errorf("commit opening: %w", err)
	}
	return w, nil
}

// Process uses the same DB transaction boundary as the SQS consumer.
func (s *Service) Process(ctx context.Context, c Command, key string) (Result, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("begin operation: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := s.ProcessInTx(ctx, tx, c, key)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit operation: %w", err)
	}
	return result, nil
}

// ProcessInTx is called by BOTH HTTP and SQS. The caller owns commit/rollback.
// The wallet, transaction, ledger, outbox and (for SQS) inbox commit together.
func (s *Service) ProcessInTx(ctx context.Context, tx pgx.Tx, c Command, key string) (Result, error) {
	amount, hash, err := validate(c, key)
	if err != nil {
		return Result{}, err
	}
	player, currency, current, version, err := lockWallet(ctx, tx, c.WalletID)
	if err != nil {
		return Result{}, err
	}

	// The wallet lock serializes competing requests from different Go processes.
	// A replay must not create another movement, including rejected operations.
	var oldID, oldHash, oldStatus, oldExternal, oldFailure string
	var oldBalance, oldVersion int64
	err = tx.QueryRow(ctx, `SELECT id,payload_hash,status,external_transaction_id,
      COALESCE(failure_code,''),result_balance_minor_units,result_wallet_version
      FROM wager_transactions WHERE provider_id=$1 AND idempotency_key=$2`, c.ProviderID, key).Scan(
		&oldID, &oldHash, &oldStatus, &oldExternal, &oldFailure, &oldBalance, &oldVersion)
	if err == nil {
		if oldHash != hash || oldExternal != c.ExternalTransactionID {
			return Result{}, ErrConflict
		}
		return Result{TransactionID: oldID, Status: oldStatus,
			Balance: mustAmount(oldBalance, amount.Currency()), WalletVersion: oldVersion, FailureCode: oldFailure}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("lookup idempotency: %w", err)
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wager_transactions WHERE provider_id=$1 AND external_transaction_id=$2)`,
		c.ProviderID, c.ExternalTransactionID).Scan(&exists); err != nil {
		return Result{}, fmt.Errorf("lookup external ID: %w", err)
	}
	if exists {
		return Result{}, ErrConflict
	}
	if player != c.PlayerID || currency != amount.Currency() {
		return Result{}, ErrWalletMismatch
	}

	decision, err := evaluate(ctx, tx, c, amount, current, version)
	if err != nil {
		return Result{}, err
	}
	id, err := wallet.NewID()
	if err != nil {
		return Result{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wager_transactions (
      id,provider_id,external_transaction_id,idempotency_key,payload_hash,wallet_id,player_id,round_id,game_id,
      kind,amount_minor_units,currency,reference_external_transaction_id,status,failure_code,result_balance_minor_units,
      result_wallet_version,resolved_reference_transaction_id,reference_retry_attempts,reference_next_retry_at,reference_deadline_at)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,0,
      CASE WHEN $14='PENDING_REFERENCE' THEN NOW()+INTERVAL '2 seconds' END,
      CASE WHEN $14='PENDING_REFERENCE' THEN NOW()+INTERVAL '24 hours' END)`,
		id, c.ProviderID, c.ExternalTransactionID, key, hash, c.WalletID, c.PlayerID, c.RoundID, c.GameID,
		c.Kind, amount.MinorUnits(), amount.Currency(), nilIfEmpty(c.ReferenceExternalTransactionID),
		decision.Status, nilIfEmpty(decision.Failure), decision.Balance, decision.Version, nilIfEmpty(decision.ReferenceID))
	if err != nil {
		if isUniqueViolation(err) {
			return Result{}, ErrConflict
		}
		return Result{}, fmt.Errorf("insert wager transaction: %w", err)
	}
	if err = insertMovement(ctx, tx, c.WalletID, id, c.Kind, currency, amount.MinorUnits(), current, decision); err != nil {
		return Result{}, err
	}
	if err = enqueueEvent(ctx, tx, c.WalletID, id, c.ProviderID, c.ExternalTransactionID,
		c.Kind, decision.Status, currency, decision.Balance, decision.Version); err != nil {
		return Result{}, fmt.Errorf("append outbox event: %w", err)
	}
	return decisionResult(id, currency, decision), nil
}

// decision carries all effects of one attempt; PENDING_REFERENCE has no ledger.
type decision struct {
	Status, Failure, Direction, ReferenceID string
	Balance, Version                        int64
}

func decisionResult(id, currency string, d decision) Result {
	return Result{TransactionID: id, Status: d.Status, Balance: mustAmount(d.Balance, currency),
		WalletVersion: d.Version, FailureCode: d.Failure}
}
func lockWallet(ctx context.Context, tx pgx.Tx, walletID string) (string, string, int64, int64, error) {
	var player, currency string
	var balance, version int64
	err := tx.QueryRow(ctx, `SELECT player_id,currency,balance_minor_units,version FROM wallets WHERE id=$1 FOR UPDATE`, walletID).
		Scan(&player, &currency, &balance, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, 0, ErrWalletNotFound
	}
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("lock wallet: %w", err)
	}
	return player, currency, balance, version, nil
}

// evaluate runs while the wallet is locked, even on reference retry.
// A reference is never looked up without scoping it to its provider.
func evaluate(ctx context.Context, tx pgx.Tx, c Command, amount money.Money, current, version int64) (decision, error) {
	d := decision{Status: "PROCESSED", Balance: current, Version: version}
	if c.Kind == "LOSS" {
		return d, nil
	}
	if c.Kind == "REFUND" || c.Kind == "ROLLBACK" || (c.Kind == "WIN" && c.ReferenceExternalTransactionID != "") {
		var rid, rwallet, rplayer, rround, rgame, rcurrency, rkind, rstatus string
		var ramount int64
		err := tx.QueryRow(ctx, `SELECT id,wallet_id,player_id,round_id,game_id,currency,kind,status,amount_minor_units
          FROM wager_transactions WHERE provider_id=$1 AND external_transaction_id=$2`,
			c.ProviderID, c.ReferenceExternalTransactionID).Scan(&rid, &rwallet, &rplayer, &rround, &rgame, &rcurrency, &rkind, &rstatus, &ramount)
		if errors.Is(err, pgx.ErrNoRows) {
			d.Status = "PENDING_REFERENCE"
			return d, nil
		}
		if err != nil {
			return d, fmt.Errorf("lookup reference: %w", err)
		}
		if rwallet != c.WalletID || rplayer != c.PlayerID || rround != c.RoundID ||
			rgame != c.GameID || rcurrency != amount.Currency() || (c.Kind != "WIN" && ramount != amount.MinorUnits()) {
			d.Status = "REJECTED"
			d.Failure = "REFERENCE_MISMATCH"
			return d, nil
		}
		if ((c.Kind == "REFUND" || c.Kind == "WIN") && rkind != "BET") ||
			(c.Kind == "ROLLBACK" && rkind != "BET" && rkind != "WIN" && rkind != "REFUND") {
			d.Status = "REJECTED"
			d.Failure = "INVALID_REFERENCE_KIND"
			return d, nil
		}
		if rstatus == "PENDING" || rstatus == "PENDING_REFERENCE" {
			d.Status = "PENDING_REFERENCE"
			return d, nil
		}
		if rstatus != "PROCESSED" {
			d.Status = "REJECTED"
			d.Failure = "REFERENCE_NOT_PROCESSED"
			return d, nil
		}
		if c.Kind != "WIN" {
			var reversed bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wager_transactions
              WHERE resolved_reference_transaction_id=$1 AND kind IN ('REFUND','ROLLBACK') AND status='PROCESSED')`, rid).Scan(&reversed)
			if err != nil {
				return d, fmt.Errorf("check existing reversal: %w", err)
			}
			if reversed {
				d.Status = "REJECTED"
				d.Failure = "ALREADY_REVERSED"
				return d, nil
			}
		}
		d.ReferenceID = rid
		if c.Kind == "REFUND" || c.Kind == "WIN" || rkind == "BET" {
			d.Direction = "CREDIT"
		} else {
			d.Direction = "DEBIT"
		}
	} else if c.Kind == "BET" {
		d.Direction = "DEBIT"
	} else {
		d.Direction = "CREDIT"
	}

	existing, err := money.FromMinorUnits(current, amount.Currency())
	if err != nil {
		return d, err
	}
	next := existing
	if d.Direction == "DEBIT" {
		if current < amount.MinorUnits() {
			d.Status = "REJECTED"
			if c.Kind == "ROLLBACK" {
				d.Failure = "REVERSAL_INSUFFICIENT_FUNDS"
			} else {
				d.Failure = "INSUFFICIENT_FUNDS"
			}
			return d, nil
		}
		next, err = existing.Sub(amount)
	} else {
		next, err = existing.Add(amount)
	}
	if err != nil {
		if errors.Is(err, money.ErrOverflow) {
			return d, ErrInvalid
		}
		return d, err
	}
	d.Balance = next.MinorUnits()
	d.Version++
	return d, nil
}

// insertMovement commits both balance and ledger, or neither.
func insertMovement(ctx context.Context, tx pgx.Tx, walletID, id, kind, currency string, amount, previous int64, d decision) error {
	if d.Status != "PROCESSED" || d.Direction == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE wallets SET balance_minor_units=$1,version=$2,updated_at=NOW() WHERE id=$3`,
		d.Balance, d.Version, walletID); err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}
	ledgerID, err := wallet.NewID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ledger_entries
      (id,wallet_id,transaction_id,entry_type,direction,amount_minor_units,currency,balance_before_minor_units,balance_after_minor_units)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, ledgerID, walletID, id, kind, d.Direction, amount, currency, previous, d.Balance)
	if err != nil {
		return fmt.Errorf("insert ledger: %w", err)
	}
	return nil
}

// RetryReference processes an already-persisted PENDING_REFERENCE transaction.
// Independent instances coordinate through the same wallet-level row lock.
func (s *Service) RetryReference(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var walletID string
	err = tx.QueryRow(ctx, `SELECT wallet_id FROM wager_transactions WHERE id=$1`, id).Scan(&walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	player, currency, current, version, err := lockWallet(ctx, tx, walletID)
	if err != nil {
		return err
	}
	var cmd Command
	var amountMinor int64
	var attempts int
	var deadlineExpired bool
	var due bool
	err = tx.QueryRow(ctx, `SELECT provider_id,external_transaction_id,player_id,round_id,game_id,kind,
      amount_minor_units,COALESCE(reference_external_transaction_id,''),reference_retry_attempts,
      (reference_deadline_at<=NOW()),(reference_next_retry_at<=NOW())
      FROM wager_transactions WHERE id=$1 AND status='PENDING_REFERENCE' FOR UPDATE`, id).
		Scan(&cmd.ProviderID, &cmd.ExternalTransactionID, &cmd.PlayerID, &cmd.RoundID, &cmd.GameID, &cmd.Kind,
			&amountMinor, &cmd.ReferenceExternalTransactionID, &attempts, &deadlineExpired, &due)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("reload reference: %w", err)
	}
	if !due {
		return tx.Commit(ctx)
	}
	if player != cmd.PlayerID {
		return ErrWalletMismatch
	}
	cmd.WalletID = walletID
	m, err := money.FromMinorUnits(amountMinor, currency)
	if err != nil {
		return err
	}
	d, err := evaluate(ctx, tx, cmd, m, current, version)
	if err != nil {
		return err
	}
	if d.Status == "PENDING_REFERENCE" {
		if deadlineExpired || attempts+1 >= 12 {
			d.Status = "REJECTED"
			d.Failure = "REFERENCE_NOT_FOUND"
		} else {
			// First retry in 2 seconds, then double up to 5 minutes.
			delay := referenceBackoff(attempts + 1)
			_, err = tx.Exec(ctx, `UPDATE wager_transactions SET reference_retry_attempts=reference_retry_attempts+1,
              reference_next_retry_at=NOW()+($2::bigint * INTERVAL '1 second'),updated_at=NOW()
              WHERE id=$1`, id, int64(delay.Seconds()))
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE wager_transactions SET status=$2,failure_code=$3,
      result_balance_minor_units=$4,result_wallet_version=$5,
      resolved_reference_transaction_id=$6,reference_next_retry_at=NULL,updated_at=NOW()
      WHERE id=$1`, id, d.Status, nilIfEmpty(d.Failure), d.Balance, d.Version, nilIfEmpty(d.ReferenceID))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	if err = insertMovement(ctx, tx, walletID, id, cmd.Kind, currency, amountMinor, current, d); err != nil {
		return err
	}
	if err = enqueueEvent(ctx, tx, walletID, id, cmd.ProviderID, cmd.ExternalTransactionID,
		cmd.Kind, d.Status, currency, d.Balance, d.Version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func referenceBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<attempt) * time.Second
}

// RetryDueReferences is a bounded sweep. Pending operations stay persisted
// across restarts; multiple workers can race safely on the same wallet.
func (s *Service) RetryDueReferences(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT id FROM wager_transactions
      WHERE status='PENDING_REFERENCE' AND reference_next_retry_at<=NOW()
      ORDER BY reference_next_retry_at,id LIMIT 100`)
	if err != nil {
		return err
	}
	ids := make([]string, 0, 100)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.RetryReference(ctx, id); err != nil {
			return fmt.Errorf("retry %s: %w", id, err)
		}
	}
	return nil
}

func mustAmount(minor int64, currency string) money.JSONMoney {
	m, _ := money.FromMinorUnits(minor, currency)
	return m.JSON()
}
func nilIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Service) Ledger(ctx context.Context, walletID string) ([]LedgerEntry, error) {
	if !uuidPattern.MatchString(walletID) {
		return nil, ErrInvalid
	}
	rows, err := s.DB.Query(ctx, `SELECT id,wallet_id,transaction_id,entry_type,direction,amount_minor_units,currency,
  balance_before_minor_units,balance_after_minor_units FROM ledger_entries
  WHERE wallet_id=$1 ORDER BY created_at,id LIMIT 100`, walletID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]LedgerEntry, 0)
	for rows.Next() {
		var e LedgerEntry
		var amount, oldBalance, newBalance int64
		var currency string
		if err := rows.Scan(&e.ID, &e.WalletID, &e.TransactionID, &e.EntryType, &e.Direction, &amount, &currency, &oldBalance, &newBalance); err != nil {
			return nil, err
		}
		e.Amount = mustAmount(amount, currency)
		e.BalanceBefore = mustAmount(oldBalance, currency)
		e.BalanceAfter = mustAmount(newBalance, currency)
		result = append(result, e)
	}
	return result, rows.Err()
}

// GetByExternalID performs provider-scoped transaction lookup. An external ID
// from another provider always appears as not found; it is never enumerable.
func (s *Service) GetByExternalID(ctx context.Context, providerID, externalID string) (Result, error) {
	if !validField(providerID) || !validField(externalID) {
		return Result{}, ErrInvalid
	}
	var result Result
	var amount int64
	var currency string
	var failure *string
	err := s.DB.QueryRow(ctx, `SELECT id,status,result_balance_minor_units,result_wallet_version,currency,failure_code
    FROM wager_transactions WHERE provider_id=$1 AND external_transaction_id=$2`, providerID, externalID).
		Scan(&result.TransactionID, &result.Status, &amount, &result.WalletVersion, &currency, &failure)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrTransactionNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("lookup provider transaction: %w", err)
	}
	result.Balance = mustAmount(amount, currency)
	if failure != nil {
		result.FailureCode = *failure
	}
	return result, nil
}

// enqueueEvent adds durable output in the SAME SQL transaction as the decision.
// A publisher will send these rows to SQS only AFTER commit succeeds.
func enqueueEvent(ctx context.Context, tx pgx.Tx, walletID, transactionID, providerID,
	externalID, kind, status, currency string, balance, version int64) error {
	id, err := wallet.NewID()
	if err != nil {
		return err
	}
	eventType := "wager." + strings.ToLower(status)
	if kind == "OPENING" {
		eventType = "wallet.opened"
	}
	payload, err := json.Marshal(struct {
		SchemaVersion         int       `json:"schemaVersion"`
		EventID               string    `json:"eventId"`
		EventType             string    `json:"eventType"`
		WalletID              string    `json:"walletId"`
		TransactionID         string    `json:"transactionId,omitempty"`
		ProviderID            string    `json:"providerId,omitempty"`
		ExternalTransactionID string    `json:"externalTransactionId,omitempty"`
		Kind                  string    `json:"kind"`
		Status                string    `json:"status"`
		Currency              string    `json:"currency"`
		BalanceMinorUnits     int64     `json:"balanceMinorUnits"`
		WalletVersion         int64     `json:"walletVersion"`
		OccurredAt            time.Time `json:"occurredAt"`
	}{1, id, eventType, walletID, transactionID, providerID, externalID, kind, status,
		currency, balance, version, time.Now().UTC()})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sqs_outbox
	(id,aggregate_id,transaction_id,event_type,payload) VALUES($1,$2,$3,$4,$5)`,
		id, walletID, nilIfEmpty(transactionID), eventType, string(payload))
	return err
}
