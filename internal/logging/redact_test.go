package logging

import (
	"strings"
	"testing"
)

func TestRedactPhoneAndJID(t *testing.T) {
	got := Redact("sender=380506171414@s.whatsapp.net phone=+380506171414")
	if strings.Contains(got, "506171") {
		t.Fatalf("redaction leaked middle digits: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("redaction did not mask value: %q", got)
	}
}
