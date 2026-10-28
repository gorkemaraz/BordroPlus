package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestSensitiveAttributesRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info")
	log.Info("login", "email", "a@b.c", "password", "hunter2", "Refresh_Token", "rt-123")

	out := buf.String()
	for _, secret := range []string{"hunter2", "rt-123"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked: %s", secret, out)
		}
	}
	if !strings.Contains(out, `"email":"a@b.c"`) {
		t.Fatalf("non-sensitive attribute missing: %s", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "warn").Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("info logged at warn level: %s", buf.String())
	}
}
