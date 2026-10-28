package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "bordroplus"

// Principal is the authenticated caller, attached to the request context.
type Principal struct {
	UserID     int64  `json:"id"`
	Email      string `json:"email"`
	Role       Role   `json:"role"`
	EmployeeID *int64 `json:"employee_id"`
	// TeamPaths are the org unit paths whose sub-trees the user may act on as
	// a manager: units they manage plus units of managers who delegated to
	// them today. Loaded per request, never taken from the token.
	TeamPaths []string `json:"team_paths"`
}

// Can reports whether the principal may use perm. Team permissions are also
// granted to an active delegate, whatever their role.
func (p Principal) Can(perm Permission) bool {
	if p.Role.Can(perm) {
		return true
	}
	_, team := teamPermissions[perm]
	return team && len(p.TeamPaths) > 0
}

// Permissions lists the effective permissions, including delegated ones.
func (p Principal) Permissions() []Permission {
	out := p.Role.Permissions()
	if len(p.TeamPaths) > 0 {
		for perm := range teamPermissions {
			out = append(out, perm)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type accessClaims struct {
	Role Role `json:"role"`
	jwt.RegisteredClaims
}

// Tokens issues and verifies short-lived HS256 access tokens.
type Tokens struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokens(secret []byte, ttl time.Duration) *Tokens {
	return &Tokens{secret: secret, ttl: ttl, now: time.Now}
}

// TTL is the access token lifetime.
func (t *Tokens) TTL() time.Duration { return t.ttl }

// Issue signs an access token for p.
func (t *Tokens) Issue(p Principal) (string, error) {
	now := t.now()
	claims := accessClaims{
		Role: p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   strconv.FormatInt(p.UserID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

var errInvalidToken = errors.New("invalid access token")

// Verify checks signature, algorithm, issuer and expiry and returns the user id.
// The role in the token is informational: the middleware reloads the account
// so deactivation and role changes apply immediately.
func (t *Tokens) Verify(token string) (int64, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
		jwt.WithLeeway(5*time.Second),
	)
	if err != nil {
		return 0, errInvalidToken
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return 0, errInvalidToken
	}
	return id, nil
}

// newRefreshToken returns an opaque random token and the SHA-256 digest that
// is stored in the database. The token itself is never persisted.
func newRefreshToken() (token string, digest []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, hashRefreshToken(token), nil
}

func hashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
