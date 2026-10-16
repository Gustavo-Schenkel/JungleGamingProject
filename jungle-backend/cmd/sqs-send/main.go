package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/messaging"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"log"
	"time"
)

// Development/test-only authenticated SQS producer. It never prints secrets.
func main() {
	client := flag.String("client", "jungle-provider-alpha", "registered OAuth provider client")
	walletID := flag.String("wallet", "", "wallet UUID")
	player := flag.String("player", "", "player ID")
	external := flag.String("external", "", "external transaction ID")
	key := flag.String("key", "", "idempotency key")
	round := flag.String("round", "stage7-round", "round ID")
	game := flag.String("game", "stage7-game", "game ID")
	kind := flag.String("kind", "WIN", "BET/WIN/LOSS/REFUND/ROLLBACK")
	amount := flag.String("amount", "1.00", "decimal string")
	currency := flag.String("currency", "BRL", "ISO currency")
	ref := flag.String("ref", "", "reference external transaction ID")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	q, err := messaging.NewQueue(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = q.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	e := messaging.SignedCommand{SchemaVersion: 1, ClientID: *client, IdempotencyKey: *key,
		Command: finance.Command{ExternalTransactionID: *external, WalletID: *walletID, PlayerID: *player,
			RoundID: *round, GameID: *game, Kind: *kind, Money: money.JSONMoney{Amount: *amount, Currency: *currency},
			ReferenceExternalTransactionID: *ref}}
	if err = q.SendCommand(ctx, e); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("SQS command accepted for queue delivery: externalId=%s kind=%s (financial outcome is asynchronous)\n", *external, *kind)
}
