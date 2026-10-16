package wallet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var ErrInvalid = errors.New("invalid wallet data")
var ErrExists = errors.New("wallet already exists")
var ErrNotFound = errors.New("wallet not found")

type Wallet struct {
	ID                string `json:"id"`
	PlayerID          string `json:"playerId"`
	Currency          string `json:"currency"`
	BalanceMinorUnits int64  `json:"balanceMinorUnits"`
	Version           int64  `json:"version"`
}

type Repository struct{ DB *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{DB: db} }

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:]), nil
}

func (r *Repository) Create(ctx context.Context, playerID, currency string) (Wallet, error) {
	playerID = strings.TrimSpace(playerID)
	if playerID == "" || len(playerID) > 200 || !currencyPattern.MatchString(currency) {
		return Wallet{}, ErrInvalid
	}
	id, err := NewID()
	if err != nil {
		return Wallet{}, err
	}
	var w Wallet
	err = r.DB.QueryRow(ctx, `INSERT INTO wallets(id,player_id,currency) VALUES($1,$2,$3) RETURNING id,player_id,currency,balance_minor_units,version`, id, playerID, currency).Scan(&w.ID, &w.PlayerID, &w.Currency, &w.BalanceMinorUnits, &w.Version)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return Wallet{}, ErrExists
		}
		return Wallet{}, err
	}
	return w, nil
}
func (r *Repository) Get(ctx context.Context, id string) (Wallet, error) {
	var w Wallet
	err := r.DB.QueryRow(ctx, `SELECT id,player_id,currency,balance_minor_units,version FROM wallets WHERE id=$1`, id).Scan(&w.ID, &w.PlayerID, &w.Currency, &w.BalanceMinorUnits, &w.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Wallet{}, ErrNotFound
	}
	return w, err
}
