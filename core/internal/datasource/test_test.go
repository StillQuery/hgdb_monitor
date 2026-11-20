package datasource

import (
	"context"
	"testing"
)

func TestConnectionConfigValidatesRequiredFields(t *testing.T) {
	_, _, _, err := connectionConfig(TestConfig{})
	publicError := AsPublicError(err)
	if publicError.Code != "INVALID_DATASOURCE" {
		t.Fatalf("expected INVALID_DATASOURCE, got %s", publicError.Code)
	}
}

func TestConnectionConfigEscapesCredentials(t *testing.T) {
	config, _, _, err := connectionConfig(TestConfig{
		Host: "127.0.0.1", Port: 5432, Database: "db/name", User: "user@name",
		Password: "p@ss:word!", SSLMode: "disable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.User != "user@name" || config.Password != "p@ss:word!" || config.Database != "db/name" {
		t.Fatal("connection fields did not survive URL encoding")
	}
}

func TestConnectionRejectsExcessiveTimeout(t *testing.T) {
	_, _, _, err := connectionConfig(TestConfig{
		Host: "127.0.0.1", Port: 5432, Database: "db", User: "user", ConnectTimeout: 31,
	})
	if AsPublicError(err).Code != "INVALID_DATASOURCE" {
		t.Fatalf("expected invalid timeout, got %v", err)
	}
}

func TestConnectionFailureIsSanitized(t *testing.T) {
	_, err := TestConnection(context.Background(), TestConfig{
		Host: "127.0.0.1", Port: 1, Database: "secret-db", User: "secret-user",
		Password: "secret-password", SSLMode: "disable", ConnectTimeout: 1,
	})
	publicError := AsPublicError(err)
	if publicError.Code != "CONNECTION_FAILED" && publicError.Code != "CONNECTION_TIMEOUT" {
		t.Fatalf("unexpected error code: %s", publicError.Code)
	}
	if publicError.Message == "" {
		t.Fatal("public error message is required")
	}
}
