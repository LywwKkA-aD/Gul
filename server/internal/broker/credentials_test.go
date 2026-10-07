package broker

import (
	"strings"
	"testing"
)

func TestCredentialsRejectInvalidSigningMaterial(t *testing.T) {
	for _, cfg := range []credentials{
		{},
		{APIKey: "valid", APISecret: "short"},
		{APIKey: "bad key", APISecret: strings.Repeat("s", 32)},
		{APIKey: "valid", APISecret: strings.Repeat("s", 257)},
		{APIKey: "valid", APISecret: strings.Repeat("s", 32) + "\n"},
		{APIKey: strings.Repeat("k", 65), APISecret: strings.Repeat("s", 32)},
	} {
		if cfg.validate() == nil {
			t.Fatal("unsafe signing credentials accepted")
		}
	}
	if (credentials{APIKey: "ключ-1", APISecret: strings.Repeat("s", 32)}).validate() != nil {
		t.Fatal("valid signing credentials rejected")
	}
}
