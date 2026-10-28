package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/audit"
	"bordroplus/internal/db"
)

var (
	// ErrInvalidCredentials covers unknown e-mail, wrong password, locked and
	// deactivated accounts alike, so responses do not reveal which accounts exist.
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidRefresh     = errors.New("invalid refresh token")
	ErrAccountInactive    = errors.New("account inactive")
)

// Policy holds the tunable security settings.
type Policy struct {
	RefreshTTL      time.Duration
	MaxFailedLogins int
	LockoutDuration time.Duration
}

// Meta is request metadata recorded in the audit log.
type Meta struct {
	RequestID string
	IP        string
}

// Service implements login, refresh-token rotation and logout.
type Service struct {
	pool   *pgxpool.Pool
	tokens *Tokens
	policy Policy
}

func NewService(pool *pgxpool.Pool, tokens *Tokens, policy Policy) *Service {
	return &Service{pool: pool, tokens: tokens, policy: policy}
}

// Session is the result of a successful login or refresh.
type Session struct {
	Principal    Principal
	AccessToken  string
	RefreshToken string
}

// Login checks the password and starts a new refresh-token family. After
// MaxFailedLogins consecutive failures the account is locked for
// LockoutDuration; a locked account rejects even the correct password.
func (s *Service) Login(ctx context.Context, email, password string, meta Meta) (Session, error) {
	var (
		p              Principal
		hash           string
		active, locked bool
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, email::text, role, employee_id, password_hash, is_active, coalesce(locked_until > now(), false)
		  FROM users WHERE email = $1`, email).
		Scan(&p.UserID, &p.Email, &p.Role, &p.EmployeeID, &hash, &active, &locked)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(password, dummyHash) // equalise timing
		s.auditFailure(ctx, nil, email, "unknown_email", meta)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if p.TeamPaths, err = loadTeamPaths(ctx, s.pool, p.EmployeeID); err != nil {
		return Session{}, err
	}

	ok, err := VerifyPassword(password, hash)
	if err != nil {
		return Session{}, fmt.Errorf("verify password for user %d: %w", p.UserID, err)
	}
	switch {
	case locked:
		s.auditFailure(ctx, &p.UserID, email, "locked", meta)
		return Session{}, ErrInvalidCredentials
	case !active:
		s.auditFailure(ctx, &p.UserID, email, "inactive", meta)
		return Session{}, ErrInvalidCredentials
	case !ok:
		// Atomic increment: concurrent wrong guesses cannot undercount.
		if _, err := s.pool.Exec(ctx, `
			UPDATE users
			   SET failed_logins = failed_logins + 1,
			       locked_until  = CASE WHEN failed_logins + 1 >= $2 THEN now() + $3 * interval '1 second' ELSE locked_until END
			 WHERE id = $1`, p.UserID, s.policy.MaxFailedLogins, s.policy.LockoutDuration.Seconds()); err != nil {
			return Session{}, err
		}
		s.auditFailure(ctx, &p.UserID, email, "wrong_password", meta)
		return Session{}, ErrInvalidCredentials
	}

	var sess Session
	err = db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = now() WHERE id = $1`, p.UserID); err != nil {
			return err
		}
		var err error
		sess, err = s.startSession(ctx, tx, p, nil)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorUserID: &p.UserID, ActorRole: string(p.Role),
			Action: "auth.login", EntityType: "user", EntityID: strconv.FormatInt(p.UserID, 10),
			RequestID: meta.RequestID, IP: meta.IP,
		})
	})
	return sess, err
}

// startSession issues an access token and a refresh token. family is nil for
// a fresh login and the existing family on rotation.
func (s *Service) startSession(ctx context.Context, tx pgx.Tx, p Principal, family *string) (Session, error) {
	refresh, digest, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, COALESCE($2::uuid, gen_random_uuid()), $3, now() + $4 * interval '1 second')`,
		p.UserID, family, digest, s.policy.RefreshTTL.Seconds()); err != nil {
		return Session{}, err
	}
	access, err := s.tokens.Issue(p)
	if err != nil {
		return Session{}, err
	}
	return Session{Principal: p, AccessToken: access, RefreshToken: refresh}, nil
}

// Refresh rotates a refresh token: the presented token is marked used and a
// successor in the same family is issued. Presenting an already-used token is
// treated as theft and revokes the entire family. The row lock serialises
// concurrent refreshes of the same token, so at most one of them succeeds.
func (s *Service) Refresh(ctx context.Context, token string, meta Meta) (Session, error) {
	if token == "" {
		return Session{}, ErrInvalidRefresh
	}
	var (
		sess   Session
		reject bool // set when the transaction must commit a revocation and still fail
	)
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var (
			id                     int64
			userID                 int64
			family                 string
			expired, used, revoked bool
		)
		err := tx.QueryRow(ctx, `
			SELECT id, user_id, family_id::text, expires_at <= now(), used_at IS NOT NULL, revoked_at IS NOT NULL
			  FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, hashRefreshToken(token)).
			Scan(&id, &userID, &family, &expired, &used, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidRefresh
		}
		if err != nil {
			return err
		}
		if revoked || expired {
			return ErrInvalidRefresh
		}
		if used {
			reject = true
			if err := revokeFamily(ctx, tx, family); err != nil {
				return err
			}
			return audit.Write(ctx, tx, audit.Entry{
				ActorUserID: &userID, Action: "auth.refresh_reuse_detected",
				EntityType: "user", EntityID: strconv.FormatInt(userID, 10),
				New: map[string]string{"family_id": family}, RequestID: meta.RequestID, IP: meta.IP,
			})
		}

		p, err := loadPrincipal(ctx, tx, userID)
		if errors.Is(err, ErrAccountInactive) {
			reject = true // not theft, but the family must die all the same
			return revokeFamily(ctx, tx, family)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		sess, err = s.startSession(ctx, tx, p, &family)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if reject {
		// The revocation above is committed; the caller still gets a 401.
		return Session{}, ErrInvalidRefresh
	}
	return sess, nil
}

// Logout revokes the refresh-token family the token belongs to. Unknown
// tokens are ignored so logout is idempotent.
func (s *Service) Logout(ctx context.Context, token string, meta Meta) error {
	if token == "" {
		return nil
	}
	return db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var userID int64
		var family string
		err := tx.QueryRow(ctx, `SELECT user_id, family_id::text FROM refresh_tokens WHERE token_hash = $1`,
			hashRefreshToken(token)).Scan(&userID, &family)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := revokeFamily(ctx, tx, family); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorUserID: &userID, Action: "auth.logout", EntityType: "user", EntityID: strconv.FormatInt(userID, 10),
			RequestID: meta.RequestID, IP: meta.IP,
		})
	})
}

// Principal loads the current state of an active account.
func (s *Service) Principal(ctx context.Context, userID int64) (Principal, error) {
	return loadPrincipal(ctx, s.pool, userID)
}

func loadPrincipal(ctx context.Context, q db.Querier, userID int64) (Principal, error) {
	var p Principal
	var active bool
	err := q.QueryRow(ctx, `SELECT id, email::text, role, employee_id, is_active FROM users WHERE id = $1`, userID).
		Scan(&p.UserID, &p.Email, &p.Role, &p.EmployeeID, &active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return Principal{}, ErrAccountInactive
	}
	if err != nil {
		return Principal{}, err
	}
	p.TeamPaths, err = loadTeamPaths(ctx, q, p.EmployeeID)
	return p, err
}

// loadTeamPaths returns the paths of units the employee manages, plus those
// managed by anyone who delegated to them for today (Europe/Istanbul date,
// the session time zone). Indexes: org_units_manager_idx and the
// delegate GiST index.
func loadTeamPaths(ctx context.Context, q db.Querier, employeeID *int64) ([]string, error) {
	if employeeID == nil {
		return []string{}, nil
	}
	rows, err := q.Query(ctx, `
		SELECT path::text FROM org_units WHERE manager_id = $1
		UNION
		SELECT u.path::text
		  FROM manager_delegations d
		  JOIN org_units u ON u.manager_id = d.manager_employee_id
		 WHERE d.delegate_employee_id = $1 AND d.revoked_at IS NULL AND d.valid_during @> current_date
		ORDER BY 1`, *employeeID)
	if err != nil {
		return nil, err
	}
	paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if paths == nil {
		paths = []string{}
	}
	return paths, err
}

func revokeFamily(ctx context.Context, tx pgx.Tx, family string) error {
	_, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, family)
	return err
}

// auditFailure records a failed login outside any transaction. A failure to
// audit must not turn a 401 into a 500, so errors are only logged here.
func (s *Service) auditFailure(ctx context.Context, userID *int64, email, reason string, meta Meta) {
	entityID := ""
	if userID != nil {
		entityID = strconv.FormatInt(*userID, 10)
	}
	err := audit.Write(ctx, s.pool, audit.Entry{
		ActorUserID: userID, Action: "auth.login_failed", EntityType: "user", EntityID: entityID,
		New: map[string]string{"email": email, "reason": reason}, RequestID: meta.RequestID, IP: meta.IP,
	})
	if err != nil {
		slog.ErrorContext(ctx, "audit login failure", "err", err)
	}
}
