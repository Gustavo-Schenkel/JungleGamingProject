package config

import (
	"net/url"
	"testing"
)

func TestLoadEncodesSpecialCharactersInDatabasePassword(t *testing.T) {
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_PORT", "5433")
	t.Setenv("POSTGRES_DB", "jungle")
	t.Setenv("POSTGRES_USER", "test_user")
	t.Setenv("POSTGRES_PASSWORD", "special@pass:#/?")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pass, exists := parsed.User.Password()
	if !exists || pass != "special@pass:#/?" || parsed.Host != "127.0.0.1:5433" {
		t.Fatal("DSN must preserve credential and configured PostgreSQL 16 Docker port")
	}
}
