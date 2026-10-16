package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	HTTPAddress         string
	Environment         string
	DatabaseURL         string
	OIDCIssuer          string
	OIDCAudience        string
	OIDCInternalClient  string
	OIDCProviderClients map[string]string
	SQSEnabled          bool
	SQSEndpoint         string
	SQSRegion           string
	SQSIncomingQueue    string
	SQSOutgoingQueue    string
	SQSDLQName          string
	SQSCommandSecrets   map[string]string
}

// Load reads the PROCESS environment. The PowerShell start-stage4.ps1 script
// explicitly loads .env before launching Go; Go itself does not read .env.
func Load() (Config, error) {
	host := getEnv("POSTGRES_HOST", "127.0.0.1")
	port := getEnv("POSTGRES_PORT", "5433")
	db := getEnv("POSTGRES_DB", "jungle")
	user := getEnv("POSTGRES_USER", "postgres")
	password := getEnv("POSTGRES_PASSWORD", "")
	sslMode := getEnv("POSTGRES_SSLMODE", "disable")
	uri := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + db,
		User:   url.UserPassword(user, password),
	}
	query := uri.Query()
	query.Set("sslmode", sslMode)
	uri.RawQuery = query.Encode()
	providerClients := make(map[string]string)
	for _, item := range strings.Split(getEnv("OIDC_PROVIDER_CLIENTS", ""), ",") {
		if item == "" {
			continue
		}
		pair := strings.Split(item, ":")
		if len(pair) != 2 || strings.TrimSpace(pair[0]) != pair[0] || strings.TrimSpace(pair[1]) != pair[1] || pair[0] == "" || pair[1] == "" {
			return Config{}, fmt.Errorf("invalid OIDC_PROVIDER_CLIENTS entry: %q", item)
		}
		if _, exists := providerClients[pair[0]]; exists {
			return Config{}, fmt.Errorf("duplicate OIDC client mapping")
		}
		providerClients[pair[0]] = pair[1]
	}
	internal := getEnv("OIDC_INTERNAL_CLIENT", "")
	if _, exists := providerClients[internal]; exists {
		return Config{}, fmt.Errorf("internal client cannot be provider client")
	}
	enabled := getEnv("SQS_ENABLED", "false") == "true"
	secrets := make(map[string]string)
	for _, item := range strings.Split(getEnv("SQS_COMMAND_KEYS", ""), ",") {
		if item == "" {
			continue
		}
		pair := strings.SplitN(item, ":", 2)
		if len(pair) != 2 || pair[0] == "" || len(pair[1]) < 32 || providerClients[pair[0]] == "" {
			return Config{}, fmt.Errorf("invalid SQS_COMMAND_KEYS mapping (expected allowlisted client and >=32-character secret)")
		}
		if _, ok := secrets[pair[0]]; ok {
			return Config{}, fmt.Errorf("duplicate SQS command key")
		}
		secrets[pair[0]] = pair[1]
	}
	if enabled && len(secrets) != len(providerClients) {
		return Config{}, fmt.Errorf("SQS_COMMAND_KEYS must include one strong key per provider client")
	}
	return Config{
		HTTPAddress:         getEnv("APP_ADDR", "127.0.0.1:8080"),
		Environment:         getEnv("APP_ENV", "development"),
		DatabaseURL:         uri.String(),
		OIDCIssuer:          getEnv("OIDC_ISSUER", ""),
		OIDCAudience:        getEnv("OIDC_AUDIENCE", ""),
		OIDCInternalClient:  internal,
		OIDCProviderClients: providerClients,
		SQSEnabled:          enabled,
		SQSEndpoint:         getEnv("SQS_ENDPOINT", ""),
		SQSRegion:           getEnv("AWS_REGION", "us-east-1"),
		SQSIncomingQueue:    getEnv("SQS_INCOMING_QUEUE", "jungle-commands.fifo"),
		SQSOutgoingQueue:    getEnv("SQS_OUTGOING_QUEUE", "jungle-events.fifo"),
		SQSDLQName:          getEnv("SQS_DLQ", "jungle-commands-dlq.fifo"),
		SQSCommandSecrets:   secrets,
	}, nil
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
