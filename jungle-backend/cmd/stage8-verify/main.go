// Command stage8-verify validates independent HTTP processes and shared
// PostgreSQL/SQS behavior using fresh wallets and IDs on every invocation.
// It is a LOCAL integration harness, not a production API endpoint.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/messaging"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/money"
	"github.com/jackc/pgx/v5/pgxpool"
)

type evidence struct {
	Stage          int               `json:"stage"`
	Mode           string            `json:"mode"`
	RunID          string            `json:"runId"`
	GeneratedAtUTC string            `json:"generatedAtUtc"`
	Endpoints      []string          `json:"endpoints"`
	Checks         map[string]string `json:"checks"`
	Wallets        []string          `json:"wallets"`
	Status         string            `json:"status"`
	Error          string            `json:"error,omitempty"`
}

type harness struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	client   *http.Client
	urls     []string
	internal string
	provider string
	ctx      context.Context
	e        evidence
}

type walletReply struct {
	ID                string `json:"id"`
	PlayerID          string `json:"playerId"`
	Currency          string `json:"currency"`
	BalanceMinorUnits int64  `json:"balanceMinorUnits"`
	Version           int64  `json:"version"`
}

type outcome struct {
	HTTP int
	Data finance.Result
	Err  error
}

func main() {
	urlsFlag := flag.String("urls", "http://127.0.0.1:8080,http://127.0.0.1:8082,http://127.0.0.1:8083", "comma-separated independent API addresses")
	mode := flag.String("mode", "full", "full (three nodes) or failover (two survivors)")
	out := flag.String("out", ".stage8-evidence/result.json", "local JSON report path, without secrets")
	timeout := flag.Duration("timeout", 150*time.Second, "overall integration test deadline")
	flag.Parse()
	addresses := strings.Split(*urlsFlag, ",")
	for i := range addresses {
		addresses[i] = strings.TrimRight(strings.TrimSpace(addresses[i]), "/")
	}
	code := run(*mode, addresses, *timeout, *out)
	os.Exit(code)
}

func run(mode string, urls []string, timeout time.Duration, report string) int {
	id, err := freshID()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	e := evidence{Stage: 8, Mode: mode, RunID: id, GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		Endpoints: urls, Checks: make(map[string]string), Status: "FAILED"}
	// Persist audit evidence on success AND failure. Do not include secrets.
	defer func() {
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode evidence:", err)
			return
		}
		if err := os.MkdirAll(filepath.Dir(report), 0700); err != nil {
			fmt.Fprintln(os.Stderr, "create evidence directory:", err)
			return
		}
		if err := os.WriteFile(report, append(data, '\n'), 0600); err != nil {
			fmt.Fprintln(os.Stderr, "write evidence:", err)
		}
	}()
	fatal := func(err error) int { e.Error = err.Error(); fmt.Fprintln(os.Stderr, "STAGE 8 FAILED:", err); return 1 }
	if mode != "full" && mode != "failover" {
		return fatal(fmt.Errorf("unknown mode %q", mode))
	}
	expected := 3
	if mode == "failover" {
		expected = 2
	}
	if len(urls) != expected {
		return fatal(fmt.Errorf("mode %s requires exactly %d independent endpoints", mode, expected))
	}
	seen := make(map[string]bool)
	for _, u := range urls {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.Path != "" {
			return fatal(fmt.Errorf("local development only: invalid API URL %q", u))
		}
		if seen[u] {
			return fatal(fmt.Errorf("duplicate API endpoint: %s", u))
		}
		seen[u] = true
	}
	cfg, err := config.Load()
	if err != nil {
		return fatal(fmt.Errorf("load config: %w", err))
	}
	sqsURL, parseErr := url.Parse(cfg.SQSEndpoint)
	if parseErr != nil || cfg.Environment != "development" || sqsURL.Scheme != "http" || sqsURL.Hostname() != "127.0.0.1" || !cfg.SQSEnabled {
		return fatal(errors.New("Stage 8 harness requires local development SQS/LocalStack"))
	}
	issuerURL, parseErr := url.Parse(cfg.OIDCIssuer)
	if parseErr != nil || issuerURL.Scheme != "http" || (issuerURL.Hostname() != "localhost" && issuerURL.Hostname() != "127.0.0.1") || issuerURL.Path != "/realms/jungle" || (os.Getenv("POSTGRES_HOST") != "127.0.0.1" && os.Getenv("POSTGRES_HOST") != "localhost") {
		return fatal(errors.New("Stage 8 requires local Keycloak and PostgreSQL"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fatal(fmt.Errorf("database: %w", err))
	}
	defer pool.Close()
	h := &harness{cfg: cfg, pool: pool, urls: urls, ctx: ctx, client: &http.Client{Timeout: 12 * time.Second}, e: e}
	if err := h.verify(); err != nil {
		e = h.e
		return fatal(err)
	}
	e = h.e
	e.Status = "PASSED"
	fmt.Printf("STAGE 8 %s PASSED: %d API endpoints, PostgreSQL concurrency, idempotency, SQS recovery. Evidence: %s\n", strings.ToUpper(mode), len(urls), report)
	return 0
}

func (h *harness) step(name string, fn func() error) error {
	if err := fn(); err != nil {
		h.e.Checks[name] = "FAIL"
		return fmt.Errorf("%s: %w", name, err)
	}
	h.e.Checks[name] = "PASS"
	fmt.Printf("PASS: %s\n", name)
	return nil
}

func (h *harness) verify() error {
	if err := h.step("Every independent API endpoint ready", func() error {
		if err := h.pool.Ping(h.ctx); err != nil {
			return err
		}
		for _, base := range h.urls {
			status, body, err := h.call("GET", base+"/health/ready", "", "", nil)
			if err != nil {
				return err
			}
			if status != 200 || !bytes.Contains(body, []byte(`"ready"`)) {
				return fmt.Errorf("%s not ready: HTTP %d %s", base, status, body)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := h.step("Keycloak service tokens", func() error {
		var err error
		h.internal, err = h.token("jungle-internal", os.Getenv("KEYCLOAK_INTERNAL_SECRET"))
		if err != nil {
			return err
		}
		h.provider, err = h.token("jungle-provider-alpha", os.Getenv("KEYCLOAK_PROVIDER_ALPHA_SECRET"))
		return err
	}); err != nil {
		return err
	}
	if err := h.step("Cross-process double-spend 100.00 / 80.00 + 80.00", h.doubleSpend); err != nil {
		return err
	}
	if err := h.step("Same-key replay through separate HTTP processes", h.concurrentReplay); err != nil {
		return err
	}
	if err := h.step("Shared SQS consumers + durable inbox/outbox", h.sqs); err != nil {
		return err
	}
	if err := h.step("Global wallet/ledger audit has zero differences", func() error {
		var n int
		if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM stage4_wallet_audit WHERE balance_minor_units<>ledger_net_minor_units`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("%d wallets have divergent ledger", n)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (h *harness) token(clientID, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("missing secret for %s in local .env", clientID)
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}}
	req, err := http.NewRequestWithContext(h.ctx, "POST", h.cfg.OIDCIssuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Keycloak HTTP %d for %s (check .env secret)", resp.StatusCode, clientID)
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&t); err != nil {
		return "", err
	}
	if t.AccessToken == "" {
		return "", errors.New("Keycloak did not return access_token")
	}
	return t.AccessToken, nil
}

func (h *harness) call(method, endpoint, token, idempotency string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(h.ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	return resp.StatusCode, data, err
}

func freshID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *harness) opening(base, suffix string) (walletReply, error) {
	player := "stage8-" + suffix + "-" + h.e.RunID
	body, _ := json.Marshal(map[string]any{"playerId": player, "initialBalance": money.JSONMoney{Amount: "100.00", Currency: "BRL"}})
	status, data, err := h.call("POST", base+"/internal/dev/wallets/open", h.internal, "", body)
	if err != nil {
		return walletReply{}, err
	}
	if status != 201 {
		return walletReply{}, fmt.Errorf("wallet opening HTTP %d: %s", status, data)
	}
	var w walletReply
	if err := json.Unmarshal(data, &w); err != nil {
		return w, err
	}
	if w.ID == "" || w.PlayerID != player || w.BalanceMinorUnits != 10000 || w.Version != 1 {
		return w, fmt.Errorf("unexpected opening: %+v", w)
	}
	h.e.Wallets = append(h.e.Wallets, w.ID)
	return w, nil
}

func (h *harness) bet(base string, w walletReply, external, key, amount string) outcome {
	payload, _ := json.Marshal(finance.Command{ExternalTransactionID: external, PlayerID: w.PlayerID, WalletID: w.ID,
		RoundID: "round-" + h.e.RunID, GameID: "stage8", Kind: "BET", Money: money.JSONMoney{Amount: amount, Currency: "BRL"}})
	status, data, err := h.call("POST", base+"/v1/transactions", h.provider, key, payload)
	if err != nil {
		return outcome{Err: err}
	}
	var result finance.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return outcome{Err: fmt.Errorf("HTTP %d unparseable reply %s: %w", status, data, err)}
	}
	return outcome{HTTP: status, Data: result}
}

func (h *harness) queryWallet(id string, balance, version int64, ledgerSize int, processedBET, rejectedBET int) error {
	var b, v int64
	if err := h.pool.QueryRow(h.ctx, `SELECT balance_minor_units,version FROM wallets WHERE id=$1`, id).Scan(&b, &v); err != nil {
		return err
	}
	if b != balance || v != version {
		return fmt.Errorf("wallet balance/version %d/%d; expected %d/%d", b, v, balance, version)
	}
	var entries, ok, rejected, auditDiff int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id=$1`, id).Scan(&entries); err != nil {
		return err
	}
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FILTER (WHERE status='PROCESSED' AND kind='BET'), count(*) FILTER (WHERE status='REJECTED' AND kind='BET') FROM wager_transactions WHERE wallet_id=$1`, id).Scan(&ok, &rejected); err != nil {
		return err
	}
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM stage4_wallet_audit WHERE wallet_id=$1 AND balance_minor_units<>ledger_net_minor_units`, id).Scan(&auditDiff); err != nil {
		return err
	}
	if entries != ledgerSize || ok != processedBET || rejected != rejectedBET || auditDiff != 0 {
		return fmt.Errorf("ledger/wager/audit entries=%d processedBET=%d rejectedBET=%d differences=%d (expected %d/%d/%d/0)", entries, ok, rejected, auditDiff, ledgerSize, processedBET, rejectedBET)
	}
	return nil
}

func (h *harness) doubleSpend() error {
	w, err := h.opening(h.urls[0], "double-spend")
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]outcome, 2)
	externals := []string{"stage8-a-" + h.e.RunID, "stage8-b-" + h.e.RunID}
	keys := []string{"stage8-key-a-" + h.e.RunID, "stage8-key-b-" + h.e.RunID}
	// In failover mode use the two surviving endpoints. In full mode send
	// simultaneous bets to processes 2 and 3; process 1 created the wallet.
	ports := []string{h.urls[len(h.urls)-2], h.urls[len(h.urls)-1]}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; results[i] = h.bet(ports[i], w, externals[i], keys[i], "80.00") }(i)
	}
	close(start)
	wg.Wait()
	processed, rejected := 0, 0
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
		switch {
		case r.HTTP == 200 && r.Data.Status == "PROCESSED" && r.Data.Balance.Amount == "20.00" && r.Data.WalletVersion == 2:
			processed++
		case r.HTTP == 422 && r.Data.Status == "REJECTED" && r.Data.FailureCode == "INSUFFICIENT_FUNDS" && r.Data.Balance.Amount == "20.00" && r.Data.WalletVersion == 2:
			rejected++
		default:
			return fmt.Errorf("unexpected competing wager outcome: HTTP %d %+v", r.HTTP, r.Data)
		}
	}
	if processed != 1 || rejected != 1 {
		return fmt.Errorf("expected one processed and one rejected, got %d/%d", processed, rejected)
	}
	if err := h.queryWallet(w.ID, 2000, 2, 2, 1, 1); err != nil {
		return err
	}
	// Replays cross another API process, including the stable HTTP 422 response.
	for i, first := range results {
		again := h.bet(h.urls[0], w, externals[i], keys[i], "80.00")
		if again.Err != nil {
			return again.Err
		}
		if again.HTTP != first.HTTP || again.Data != first.Data {
			return fmt.Errorf("cross-instance replay mismatch for %s: %+v != %+v", externals[i], again, first)
		}
	}
	return h.queryWallet(w.ID, 2000, 2, 2, 1, 1)
}

func (h *harness) concurrentReplay() error {
	w, err := h.opening(h.urls[0], "shared-key")
	if err != nil {
		return err
	}
	external := "stage8-replay-" + h.e.RunID
	key := "stage8-replay-key-" + h.e.RunID
	const n = 30
	responses := make([]outcome, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			responses[i] = h.bet(h.urls[i%len(h.urls)], w, external, key, "25.00")
		}(i)
	}
	close(start)
	wg.Wait()
	id := ""
	for i, r := range responses {
		if r.Err != nil {
			return fmt.Errorf("replay %d: %w", i, r.Err)
		}
		if r.HTTP != 200 || r.Data.Status != "PROCESSED" || r.Data.Balance.Amount != "75.00" || r.Data.WalletVersion != 2 {
			return fmt.Errorf("replay %d unexpected: HTTP %d %+v", i, r.HTTP, r.Data)
		}
		if id == "" {
			id = r.Data.TransactionID
		}
		if id != r.Data.TransactionID {
			return fmt.Errorf("replay changed transaction ID %s/%s", id, r.Data.TransactionID)
		}
	}
	return h.queryWallet(w.ID, 7500, 2, 2, 1, 0)
}

func (h *harness) sqs() error {
	w, err := h.opening(h.urls[0], "sqs-workers")
	if err != nil {
		return err
	}
	queue, err := messaging.NewQueue(h.cfg)
	if err != nil {
		return err
	}
	if err := queue.Connect(h.ctx); err != nil {
		return err
	}
	external := "stage8-sqs-bet-" + h.e.RunID
	key := "stage8-sqs-key-" + h.e.RunID
	msg := messaging.SignedCommand{SchemaVersion: 1, ClientID: "jungle-provider-alpha", IdempotencyKey: key,
		Command: finance.Command{ExternalTransactionID: external, PlayerID: w.PlayerID, WalletID: w.ID,
			RoundID: "sqs-" + h.e.RunID, GameID: "stage8", Kind: "BET", Money: money.JSONMoney{Amount: "30.00", Currency: "BRL"}}}
	if err := queue.SendCommand(h.ctx, msg); err != nil {
		return fmt.Errorf("send SQS: %w", err)
	}
	if err := h.waitFor(50*time.Second, func() (bool, error) {
		status, body, err := h.call("GET", h.urls[len(h.urls)-1]+"/v1/transactions/"+external, h.provider, "", nil)
		if err != nil {
			return false, err
		}
		if status == 404 {
			return false, nil
		}
		if status != 200 {
			return false, fmt.Errorf("SQS result HTTP %d: %s", status, body)
		}
		var r finance.Result
		if err := json.Unmarshal(body, &r); err != nil {
			return false, err
		}
		return r.Status == "PROCESSED" && r.Balance.Amount == "70.00" && r.WalletVersion == 2, nil
	}); err != nil {
		return fmt.Errorf("wait first SQS result: %w", err)
	}
	if err := queue.SendCommand(h.ctx, msg); err != nil {
		return fmt.Errorf("SQS replay: %w", err)
	}
	if err := h.waitFor(70*time.Second, func() (bool, error) {
		var inbox, sent, published int
		err := h.pool.QueryRow(h.ctx, `SELECT
		 (SELECT count(*) FROM sqs_inbox WHERE idempotency_key=$1 AND provider_id='provider-alpha'),
		 (SELECT count(*) FROM sqs_outbox WHERE aggregate_id=$2),
		 (SELECT count(*) FROM sqs_outbox WHERE aggregate_id=$2 AND state='PUBLISHED')`, key, w.ID).Scan(&inbox, &sent, &published)
		if err != nil {
			return false, err
		}
		return inbox >= 2 && sent == 2 && published == 2, nil
	}); err != nil {
		return fmt.Errorf("SQS duplicate inbox/outbox publish: %w", err)
	}
	return h.queryWallet(w.ID, 7000, 2, 2, 1, 0)
}

func (h *harness) waitFor(max time.Duration, check func() (bool, error)) error {
	end := time.NewTimer(max)
	defer end.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-h.ctx.Done():
			return h.ctx.Err()
		case <-end.C:
			return errors.New("condition not met before timeout")
		case <-tick.C:
		}
	}
}

// The harness deliberately uses independent HTTP sockets; direct SQL is
// strictly read-only and corroborates results (it never creates bets).
