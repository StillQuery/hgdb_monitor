package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnonymousIDDoesNotEmbedSourceAndKeepsEmptyOptionalFieldsEmpty(t *testing.T) {
	if anonymousID("db_reference", "user", "") != "" {
		t.Fatal("empty optional identity should remain empty")
	}
	value := anonymousID("db_reference", "user", "private_account")
	if value == "" || strings.Contains(value, "private") || strings.Contains(value, "account") {
		t.Fatalf("anonymous identity leaked source text: %q", value)
	}
	if value != anonymousID("db_reference", "user", "private_account") {
		t.Fatal("anonymous identity must remain stable within an instance")
	}
}

func TestAIActivityDTOHasNoRawConnectionIdentityFields(t *testing.T) {
	encoded, err := json.Marshal(AIActivityItem{})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"clientaddress", "hostname", "password", "username", "applicationname"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("AI DTO contains forbidden field %q: %s", forbidden, text)
		}
	}
}
