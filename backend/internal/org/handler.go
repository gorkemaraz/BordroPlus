package org

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/audit"
	"bordroplus/internal/auth"
	"bordroplus/internal/httpx"
	"bordroplus/internal/secret"
)

const dateLayout = "2006-01-02"

// Handler serves /org, /employees and /delegations. All routes expect
// auth.Authenticate to have run.
type Handler struct {
	pool *pgxpool.Pool
	box  *secret.Box
	now  func() time.Time
}

func NewHandler(pool *pgxpool.Pool, box *secret.Box) *Handler {
	return &Handler{pool: pool, box: box, now: time.Now}
}

// Routes mounts every route with its permission. idem is the
// Idempotency-Key middleware applied to state-changing POSTs.
func (h *Handler) Routes(r chi.Router, idem func(http.Handler) http.Handler) {
	need := auth.Require

	r.With(need(auth.PermOrgRead)).Get("/org/units", httpx.Handle(h.listUnits))
	r.With(need(auth.PermOrgWrite), idem).Post("/org/units", httpx.Handle(h.createUnit))
	r.With(need(auth.PermOrgWrite)).Patch("/org/units/{id}", httpx.Handle(h.updateUnit))

	// Scope (self/team/all) is applied inside the handlers.
	r.Get("/employees", httpx.Handle(h.listEmployees))
	r.Get("/employees/{id}", httpx.Handle(h.getEmployee))
	r.With(need(auth.PermEmployeeWrite), idem).Post("/employees", httpx.Handle(h.createEmployee))
	r.With(need(auth.PermEmployeeWrite)).Patch("/employees/{id}", httpx.Handle(h.updateEmployee))
	r.Get("/employees/{id}/salaries", httpx.Handle(h.listSalaries))
	r.With(need(auth.PermSalaryWrite), idem).Post("/employees/{id}/salaries", httpx.Handle(h.addSalary))

	r.Get("/delegations", httpx.Handle(h.listDelegations))
	r.With(idem).Post("/delegations", httpx.Handle(h.createDelegation))
	r.Post("/delegations/{id}/revoke", httpx.Handle(h.revokeDelegation))
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.FromContext(r.Context())
	return p
}

// auditEntry pre-fills actor and request metadata.
func auditEntry(r *http.Request, action, entityType string, entityID int64, oldV, newV any) audit.Entry {
	p := principal(r)
	return audit.Entry{
		ActorUserID: &p.UserID, ActorRole: string(p.Role),
		Action: action, EntityType: entityType, EntityID: strconv.FormatInt(entityID, 10),
		Old: oldV, New: newV,
		RequestID: httpx.RequestID(r.Context()), IP: httpx.ClientIP(r),
	}
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		return 0, httpx.ErrNotFound
	}
	return id, nil
}

func queryID(r *http.Request, name string) (*int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return nil, httpx.Validation(map[string]string{name: "pozitif tam sayı olmalı"})
	}
	return &n, nil
}

// parseDate validates an optional YYYY-MM-DD field into fields.
func parseDate(fields map[string]string, name string, v *string) *time.Time {
	if v == nil {
		return nil
	}
	t, err := time.Parse(dateLayout, *v)
	if err != nil {
		fields[name] = "YYYY-AA-GG biçiminde olmalı"
		return nil
	}
	return &t
}

// today is the current date in Turkey, as a UTC midnight for DATE columns.
func (h *Handler) today() time.Time {
	y, m, d := h.now().In(istanbul).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// istanbul needs tz data; the binaries embed it with time/tzdata because the
// distroless image has none.
var istanbul = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return loc
}()

var (
	errNotVisible = httpx.ErrForbidden
	errConflict   = func(code, msg string) *httpx.Error { return httpx.NewError(http.StatusConflict, code, msg) }
)
