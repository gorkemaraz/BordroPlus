package timesheet

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/httpx"
	"bordroplus/internal/org"
)

const (
	maxImport   = 5000
	maxDaysSpan = 62
)

var (
	errLocked     = httpx.NewError(http.StatusConflict, "period_locked", "bu puantaj dönemi onaylanmış, değiştirilemez")
	errOpen       = httpx.NewError(http.StatusConflict, "period_not_ended", "dönem henüz bitmedi")
	errUnresolved = httpx.NewError(http.StatusConflict, "unresolved_anomalies", "dönemde onay bekleyen basım düzeltmeleri var")
	errDecided    = httpx.NewError(http.StatusConflict, "invalid_state", "öneri zaten karara bağlanmış ya da geçersiz hale gelmiş")
	errSelf       = httpx.NewError(http.StatusForbidden, "self_approval", "kendi puantajınızı onaylayamazsınız")
)

// Handler serves /timesheet.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Routes(r chi.Router, idem func(http.Handler) http.Handler) {
	r.With(auth.Require(auth.PermTimesheetManage), idem).Post("/timesheet/punches", httpx.Handle(h.importPunches))
	r.Post("/timesheet/clock", httpx.Handle(h.clock))
	r.Get("/timesheet/employees/{id}/days", httpx.Handle(h.days))
	r.Get("/timesheet/employees/{id}/months/{month}", httpx.Handle(h.month))
	r.Post("/timesheet/employees/{id}/months/{month}/approve", httpx.Handle(h.approveMonth))
	r.Get("/timesheet/corrections", httpx.Handle(h.listCorrections))
	r.Post("/timesheet/corrections/{id}/approve", httpx.Handle(h.approveCorrection))
	r.Post("/timesheet/corrections/{id}/reject", httpx.Handle(h.rejectCorrection))
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.FromContext(r.Context())
	return p
}

func actor(r *http.Request) Actor {
	p := principal(r)
	return Actor{UserID: p.UserID, Role: string(p.Role), RequestID: httpx.RequestID(r.Context()), IP: httpx.ClientIP(r)}
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		return 0, httpx.ErrNotFound
	}
	return id, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, ErrPeriodLocked):
		return errLocked
	case errors.Is(err, ErrPeriodOpen):
		return errOpen
	case errors.Is(err, ErrUnresolved):
		return errUnresolved
	case errors.Is(err, ErrAlreadyDecided):
		return errDecided
	case errors.Is(err, errOverrideRange):
		return httpx.Validation(map[string]string{"at": "önerilen saatten en fazla 12 saat farklı olabilir"})
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	}
	return err
}

// canDecide: HR for anyone, managers and active delegates for their
// sub-tree, never for oneself.
func (h *Handler) canDecide(r *http.Request, employeeID int64) error {
	p := principal(r)
	if p.EmployeeID != nil && *p.EmployeeID == employeeID {
		return errSelf
	}
	if p.Can(auth.PermTimesheetManage) {
		return nil
	}
	if p.Can(auth.PermTimesheetApproveTeam) {
		in, err := org.InTeam(r.Context(), h.svc.pool, p, employeeID)
		if err != nil {
			return err
		}
		if in {
			return nil
		}
	}
	return httpx.ErrForbidden
}

func (h *Handler) visible(r *http.Request) (int64, error) {
	id, err := pathID(r)
	if err != nil {
		return 0, err
	}
	return id, org.CheckVisible(r.Context(), h.svc.pool, principal(r), org.TimesheetData, id)
}

type punchInput struct {
	EmployeeID int64  `json:"employee_id"`
	PunchedAt  string `json:"punched_at"` // RFC 3339
	Direction  string `json:"direction"`
	DeviceID   string `json:"device_id"`
}

// importPunches loads device or manual punches (HR). The batch is
// all-or-nothing.
func (h *Handler) importPunches(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Punches []punchInput `json:"punches"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if len(in.Punches) == 0 || len(in.Punches) > maxImport {
		return httpx.Validation(map[string]string{"punches": "1-" + strconv.Itoa(maxImport) + " basım gönderilmeli"})
	}
	ps := make([]NewPunch, 0, len(in.Punches))
	f := map[string]string{}
	horizon := h.svc.now().Add(5 * time.Minute)
	for i, p := range in.Punches {
		key := "punches[" + strconv.Itoa(i) + "]"
		t, err := time.Parse(time.RFC3339, p.PunchedAt)
		switch {
		case p.EmployeeID < 1:
			f[key+".employee_id"] = "zorunlu"
		case err != nil:
			f[key+".punched_at"] = "RFC 3339 zaman damgası olmalı (ör. 2026-11-02T08:00:00+03:00)"
		case t.After(horizon):
			f[key+".punched_at"] = "gelecekte olamaz"
		case p.Direction != string(In) && p.Direction != string(Out):
			f[key+".direction"] = "in veya out"
		case len(p.DeviceID) > 64:
			f[key+".device_id"] = "en fazla 64 karakter"
		}
		if len(f) > 20 {
			break
		}
		source := "manual"
		if p.DeviceID != "" {
			source = "device"
		}
		ps = append(ps, NewPunch{EmployeeID: p.EmployeeID, At: t, Dir: Direction(p.Direction), Source: source, DeviceID: p.DeviceID})
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	inserted, dups, err := h.svc.Import(r.Context(), actor(r), ps)
	if err != nil {
		if db.PgCode(err) == "23503" {
			return httpx.Validation(map[string]string{"punches": "bilinmeyen çalışan"})
		}
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int{"inserted": inserted, "duplicates": dups})
	return nil
}

// clock is the web time clock: the server's time, the caller's own record.
func (h *Handler) clock(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if p.EmployeeID == nil || !p.Can(auth.PermTimesheetReadSelf) {
		return httpx.ErrForbidden
	}
	var in struct {
		Direction string `json:"direction"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Direction != string(In) && in.Direction != string(Out) {
		return httpx.Validation(map[string]string{"direction": "in veya out"})
	}
	now := h.svc.now()
	if _, _, err := h.svc.Import(r.Context(), actor(r), []NewPunch{{EmployeeID: *p.EmployeeID, At: now, Dir: Direction(in.Direction), Source: "web"}}); err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"punched_at": now.UTC(), "direction": in.Direction})
	return nil
}

type dayView struct {
	Date       string    `json:"date"`
	NetMinutes int       `json:"net_minutes"`
	Sessions   []Session `json:"sessions"`
}

// days: sessions per work date, anomalies and their proposals for [from, to].
func (h *Handler) days(w http.ResponseWriter, r *http.Request) error {
	id, err := h.visible(r)
	if err != nil {
		return err
	}
	f := map[string]string{}
	from, err1 := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	to, err2 := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if err1 != nil {
		f["from"] = "YYYY-AA-GG"
	}
	if err2 != nil {
		f["to"] = "YYYY-AA-GG"
	}
	if len(f) == 0 && (to.Before(from) || to.Sub(from) > maxDaysSpan*24*time.Hour) {
		f["to"] = "from ile to arası 0-" + strconv.Itoa(maxDaysSpan) + " gün olmalı"
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	a, err := h.svc.Days(r.Context(), id, from, to)
	if err != nil {
		return err
	}
	byDate := map[time.Time]*dayView{}
	days := []*dayView{}
	for _, s := range a.Sessions {
		if s.WorkDate.Before(from) || s.WorkDate.After(to) {
			continue
		}
		d := byDate[s.WorkDate]
		if d == nil {
			d = &dayView{Date: s.WorkDate.Format("2006-01-02")}
			byDate[s.WorkDate] = d
			days = append(days, d)
		}
		d.Sessions = append(d.Sessions, s)
		d.NetMinutes += s.Net
	}
	rows, err := h.svc.pool.Query(r.Context(), correctionSelect+`
		 WHERE c.employee_id = $1 AND c.work_date BETWEEN $2 AND $3 AND c.status <> 'obsolete' ORDER BY c.work_date, c.id`, id, from, to)
	if err != nil {
		return err
	}
	corr, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Correction, error) { return scanCorrection(row) })
	if err != nil {
		return err
	}
	anomalies := a.Anomalies
	if anomalies == nil {
		anomalies = []Anomaly{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"employee_id": id, "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"),
		"days": days, "anomalies": anomalies, "corrections": corr,
	})
	return nil
}

func parseMonth(r *http.Request) (time.Time, error) {
	m, err := time.Parse("2006-01", chi.URLParam(r, "month"))
	if err != nil {
		return time.Time{}, httpx.Validation(map[string]string{"month": "YYYY-AA biçiminde olmalı"})
	}
	return m, nil
}

func (h *Handler) month(w http.ResponseWriter, r *http.Request) error {
	id, err := h.visible(r)
	if err != nil {
		return err
	}
	m, err := parseMonth(r)
	if err != nil {
		return err
	}
	sum, err := h.svc.Month(r.Context(), id, m)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
	return nil
}

func (h *Handler) approveMonth(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := org.CheckVisible(r.Context(), h.svc.pool, principal(r), org.TimesheetData, id); err != nil {
		return err
	}
	if err := h.canDecide(r, id); err != nil {
		return err
	}
	m, err := parseMonth(r)
	if err != nil {
		return err
	}
	sum, err := h.svc.ApproveMonth(r.Context(), actor(r), id, m)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
	return nil
}

// listCorrections: proposals in the caller's scope, newest first. Filters:
// status (default proposed), employee_id, for_approval=true.
func (h *Handler) listCorrections(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	scope := org.ScopeFor(p, org.TimesheetData)
	if scope.Empty() {
		return httpx.ErrForbidden
	}
	q := r.URL.Query()
	limit, err := httpx.PageLimit(r, 50, 200)
	if err != nil {
		return err
	}
	var cursor, emp *int64
	for name, dst := range map[string]**int64{"cursor": &cursor, "employee_id": &emp} {
		if v := q.Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1 {
				return httpx.Validation(map[string]string{name: "pozitif tam sayı olmalı"})
			}
			*dst = &n
		}
	}
	status := q.Get("status")
	if status == "" {
		status = "proposed"
	}
	if !map[string]bool{"proposed": true, "approved": true, "rejected": true, "obsolete": true, "all": true}[status] {
		return httpx.Validation(map[string]string{"status": "proposed, approved, rejected, obsolete veya all"})
	}
	forApproval := q.Get("for_approval") == "true"
	paths := []string{}
	if p.Can(auth.PermTimesheetApproveTeam) {
		paths = p.TeamPaths
	}
	args := append(scope.Args(), cursor, emp, status, forApproval, p.Can(auth.PermTimesheetManage), paths, p.EmployeeID, limit+1)
	rows, err := h.svc.pool.Query(r.Context(), correctionSelect+`
		 WHERE `+org.ScopeSQL+`
		   AND ($4::bigint IS NULL OR c.id < $4)
		   AND ($5::bigint IS NULL OR c.employee_id = $5)
		   AND ($6 = 'all' OR c.status = $6)
		   AND (NOT $7 OR (c.employee_id IS DISTINCT FROM $10 AND ($8 OR u.path <@ ANY($9::ltree[]))))
		 ORDER BY c.id DESC LIMIT $11`, args...)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Correction, error) { return scanCorrection(row) })
	if err != nil {
		return err
	}
	page := httpx.Page[Correction]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = strconv.FormatInt(items[limit-1].ID, 10)
	}
	httpx.WriteJSON(w, http.StatusOK, page)
	return nil
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, approve bool) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		At   *string `json:"at"`
		Note string  `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	var override *time.Time
	if in.At != nil {
		if !approve {
			return httpx.Validation(map[string]string{"at": "yalnızca onayda kullanılır"})
		}
		t, err := time.Parse(time.RFC3339, *in.At)
		if err != nil {
			return httpx.Validation(map[string]string{"at": "RFC 3339 zaman damgası olmalı"})
		}
		override = &t
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 500 {
		return httpx.Validation(map[string]string{"note": "en fazla 500 karakter"})
	}
	c, err := h.svc.DecideCorrection(r.Context(), actor(r), id, approve, override, in.Note, func(c Correction) error {
		if err := org.CheckVisible(r.Context(), h.svc.pool, principal(r), org.TimesheetData, c.EmployeeID); err != nil {
			return err
		}
		return h.canDecide(r, c.EmployeeID)
	})
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

func (h *Handler) approveCorrection(w http.ResponseWriter, r *http.Request) error {
	return h.decide(w, r, true)
}

func (h *Handler) rejectCorrection(w http.ResponseWriter, r *http.Request) error {
	return h.decide(w, r, false)
}
