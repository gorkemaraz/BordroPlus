package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"bordroplus/internal/httpx"
)

// RefreshCookie carries the refresh token. It is HttpOnly (invisible to
// scripts), SameSite=Strict (not sent cross-site) and scoped to the auth
// routes, so it is only ever sent to refresh and logout.
const (
	RefreshCookie     = "bp_refresh"
	refreshCookiePath = "/api/v1/auth"
)

var (
	errBadCredentials = httpx.NewError(http.StatusUnauthorized, "invalid_credentials", "e-posta veya parola hatalı")
	errBadRefresh     = httpx.NewError(http.StatusUnauthorized, "invalid_refresh_token", "oturum süresi doldu, yeniden giriş yapın")
)

type ctxKey struct{}

// FromContext returns the authenticated principal, if any.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// WithPrincipal stores p in ctx. Exposed for handler tests.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// Handler serves the /auth routes and provides the auth middleware.
type Handler struct {
	svc          *Service
	tokens       *Tokens
	refreshTTL   time.Duration
	secureCookie bool
}

func NewHandler(svc *Service, tokens *Tokens, refreshTTL time.Duration, secureCookie bool) *Handler {
	return &Handler{svc: svc, tokens: tokens, refreshTTL: refreshTTL, secureCookie: secureCookie}
}

// Routes mounts login, refresh, logout and me.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/login", httpx.Handle(h.login))
	r.Post("/refresh", httpx.Handle(h.refresh))
	r.Post("/logout", httpx.Handle(h.logout))
	r.With(h.Authenticate).Get("/me", httpx.Handle(h.me))
}

// Authenticate requires a valid "Authorization: Bearer <access token>" and
// loads the account on every request, so a deactivated user or a changed role
// takes effect immediately instead of when the token expires.
func (h *Handler) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			httpx.WriteError(w, r, httpx.ErrUnauthorized)
			return
		}
		userID, err := h.tokens.Verify(raw)
		if err != nil {
			httpx.WriteError(w, r, httpx.ErrUnauthorized)
			return
		}
		p, err := h.svc.Principal(r.Context(), userID)
		if errors.Is(err, ErrAccountInactive) {
			httpx.WriteError(w, r, httpx.ErrUnauthorized)
			return
		}
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// Require rejects callers whose role lacks any of perms with 403. It must run
// after Authenticate; without a principal it answers 401.
func Require(perms ...Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := FromContext(r.Context())
			if !ok {
				httpx.WriteError(w, r, httpx.ErrUnauthorized)
				return
			}
			for _, perm := range perms {
				if !p.Can(perm) {
					slog.WarnContext(r.Context(), "permission denied",
						"request_id", httpx.RequestID(r.Context()), "user_id", p.UserID,
						"role", p.Role, "permission", perm, "path", r.URL.Path)
					httpx.WriteError(w, r, httpx.ErrForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// SessionResponse is returned by login and refresh. The refresh token travels
// only in the cookie.
type SessionResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	User        meView `json:"user"`
}

type meView struct {
	Principal
	Permissions []Permission `json:"permissions"`
}

func newMeView(p Principal) meView { return meView{Principal: p, Permissions: p.Permissions()} }

func (h *Handler) login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	req.Email = strings.TrimSpace(req.Email)
	fields := map[string]string{}
	if _, err := mail.ParseAddress(req.Email); err != nil || len(req.Email) > 254 {
		fields["email"] = "geçerli bir e-posta adresi girin"
	}
	if req.Password == "" || len(req.Password) > MaxPasswordLen {
		fields["password"] = "parola zorunludur"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}

	sess, err := h.svc.Login(r.Context(), req.Email, req.Password, meta(r))
	if errors.Is(err, ErrInvalidCredentials) {
		return errBadCredentials
	}
	if err != nil {
		return err
	}
	h.writeSession(w, sess)
	return nil
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) error {
	sess, err := h.svc.Refresh(r.Context(), cookieValue(r), meta(r))
	if errors.Is(err, ErrInvalidRefresh) {
		h.clearCookie(w)
		return errBadRefresh
	}
	if err != nil {
		return err
	}
	h.writeSession(w, sess)
	return nil
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	if err := h.svc.Logout(r.Context(), cookieValue(r), meta(r)); err != nil {
		return err
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	p, _ := FromContext(r.Context())
	httpx.WriteJSON(w, http.StatusOK, newMeView(p))
	return nil
}

func (h *Handler) writeSession(w http.ResponseWriter, sess Session) {
	http.SetCookie(w, &http.Cookie{
		Name: RefreshCookie, Value: sess.RefreshToken, Path: refreshCookiePath,
		MaxAge: int(h.refreshTTL.Seconds()), HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
	})
	httpx.WriteJSON(w, http.StatusOK, SessionResponse{
		AccessToken: sess.AccessToken, TokenType: "Bearer",
		ExpiresIn: int(h.tokens.TTL().Seconds()), User: newMeView(sess.Principal),
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: RefreshCookie, Path: refreshCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
	})
}

func cookieValue(r *http.Request) string {
	c, err := r.Cookie(RefreshCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func meta(r *http.Request) Meta {
	return Meta{RequestID: httpx.RequestID(r.Context()), IP: httpx.ClientIP(r)}
}
