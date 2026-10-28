package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("Doğru-Parola.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if ok, err := VerifyPassword("Doğru-Parola.1", h); err != nil || !ok {
		t.Fatalf("correct password rejected: ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword("doğru-parola.1", h); ok {
		t.Fatal("wrong password accepted")
	}

	h2, _ := HashPassword("Doğru-Parola.1")
	if h == h2 {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, h := range []string{
		"",
		"plaintext",
		"$2a$10$bcrypthashbcrypthashbcrypthashbcrypthashbcrypthash",
		"$argon2id$v=19$m=19456,t=2,p=1$!!notbase64$aGFzaA",
		"$argon2id$v=19$m=999999999,t=2,p=1$c2FsdA$aGFzaA", // absurd memory: refuse instead of allocating
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
	} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("hash %q: expected error", h)
		}
	}
}

func TestRBACMatrix(t *testing.T) {
	for _, r := range Roles {
		if !r.Valid() {
			t.Fatalf("%s not valid", r)
		}
		// Every account is also an employee and keeps self-service access.
		for _, p := range selfService {
			if !r.Can(p) {
				t.Errorf("%s lacks self-service permission %s", r, p)
			}
		}
		// Separation of duties: whoever calculates payroll cannot approve or close it.
		if r.Can(PermPayrollCalculate) && (r.Can(PermPayrollApprove) || r.Can(PermPayrollClose)) {
			t.Errorf("%s can both calculate and approve/close payroll", r)
		}
	}

	if got := len(RoleEmployee.Permissions()); got != len(selfService) {
		t.Errorf("employee has %d permissions, want only the %d self-service ones", got, len(selfService))
	}
	for _, p := range RoleAuditor.Permissions() {
		if !strings.Contains(string(p), ".read") && !strings.HasPrefix(string(p), "leave.request") {
			t.Errorf("auditor must be read-only, has %s", p)
		}
	}
	if RoleAuditor.Can(PermSalaryRead) {
		t.Error("auditor must not see plain-text salaries")
	}
	if !RoleManager.Can(PermLeaveApproveTeam) || RoleManager.Can(PermLeaveReadAll) {
		t.Error("manager must approve team leave but not read company-wide leave")
	}

	unknown := Role("admin")
	if unknown.Valid() || unknown.Can(PermOrgRead) || len(unknown.Permissions()) != 0 {
		t.Error("unknown role must grant nothing")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	tok := NewTokens([]byte(strings.Repeat("k", 32)), 15*time.Minute)
	s, err := tok.Issue(Principal{UserID: 42, Role: RoleHR})
	if err != nil {
		t.Fatal(err)
	}
	id, err := tok.Verify(s)
	if err != nil || id != 42 {
		t.Fatalf("Verify = %d, %v", id, err)
	}
	// Two tokens for the same user in the same second still differ.
	if s2, _ := tok.Issue(Principal{UserID: 42, Role: RoleHR}); s2 == s {
		t.Fatal("tokens issued in the same second are identical")
	}
}

func TestAccessTokenRejections(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	tok := NewTokens(secret, 15*time.Minute)
	valid, _ := tok.Issue(Principal{UserID: 1, Role: RoleEmployee})

	expired := NewTokens(secret, time.Minute)
	expired.now = func() time.Time { return time.Now().Add(-time.Hour) }
	old, _ := expired.Issue(Principal{UserID: 1, Role: RoleEmployee})

	otherKey, _ := NewTokens([]byte(strings.Repeat("x", 32)), time.Minute).Issue(Principal{UserID: 1})

	// alg=none must never be accepted.
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer: issuer, Subject: "1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	wrongIssuer, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "someone-else", Subject: "1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(secret)

	noExpiry, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: issuer, Subject: "1",
	}).SignedString(secret)

	cases := map[string]string{
		"expired":      old,
		"other key":    otherKey,
		"alg none":     none,
		"wrong issuer": wrongIssuer,
		"no expiry":    noExpiry,
		"tampered":     valid[:len(valid)-2] + "xx",
		"garbage":      "not.a.jwt",
	}
	for name, s := range cases {
		if _, err := tok.Verify(s); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}
}

func TestRefreshTokenDigest(t *testing.T) {
	a, da, err := newRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := newRefreshToken()
	if a == b {
		t.Fatal("refresh tokens must be unique")
	}
	if len(da) != 32 || string(da) != string(hashRefreshToken(a)) {
		t.Fatal("digest must be the SHA-256 of the token")
	}
}
