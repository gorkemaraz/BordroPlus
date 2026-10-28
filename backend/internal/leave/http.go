package leave

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/httpx"
	"bordroplus/internal/org"
)

var (
	errInsufficient = httpx.NewError(http.StatusConflict, "insufficient_balance", "yıllık izin bakiyesi yetersiz")
	errOverlap      = httpx.NewError(http.StatusConflict, "leave_overlap", "bu tarihlerle çakışan bir izin talebi var")
	errNotPending   = httpx.NewError(http.StatusConflict, "invalid_state", "talep bu işlem için uygun durumda değil")
	errNoWorkDays   = httpx.Validation(map[string]string{"end_date": "seçilen aralıkta iş günü yok"})
	errSelfDecision = httpx.NewError(http.StatusForbidden, "self_approval", "kendi izin talebinizi onaylayamaz ya da reddedemezsiniz")
)

// Handler serves /leave.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes mounts the leave API. idem is the Idempotency-Key middleware.
func (h *Handler) Routes(r chi.Router, idem func(http.Handler) http.Handler) {
	r.Get("/leave/holidays", httpx.Handle(h.listHolidays))
	r.With(auth.Require(auth.PermLeaveManage)).Post("/leave/holidays", httpx.Handle(h.addHoliday))

	r.Get("/leave/employees/{id}/balance", httpx.Handle(h.balance))
	r.Get("/leave/employees/{id}/ledger", httpx.Handle(h.ledger))

	r.Get("/leave/requests", httpx.Handle(h.list))
	r.Get("/leave/requests/{id}", httpx.Handle(h.get))
	r.With(idem).Post("/leave/requests", httpx.Handle(h.create))
	r.With(idem).Post("/leave/requests/{id}/approve", httpx.Handle(h.approve))
	r.Post("/leave/requests/{id}/reject", httpx.Handle(h.reject))
	r.Post("/leave/requests/{id}/cancel", httpx.Handle(h.cancel))
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

// mapErr turns service errors into API errors.
func mapErr(err error) error {
	switch {
	case errors.Is(err, ErrInsufficientBalance):
		return errInsufficient
	case errors.Is(err, ErrOverlap):
		return errOverlap
	case errors.Is(err, ErrNotPending):
		return errNotPending
	case errors.Is(err, ErrNoWorkingDays):
		return errNoWorkDays
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	}
	return err
}

func parseDate(f map[string]string, name, v string) time.Time {
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		f[name] = "YYYY-AA-GG biçiminde olmalı"
	}
	return t
}

// ---- holidays ----

type holiday struct {
	Date    string `json:"date"`
	Name    string `json:"name"`
	HalfDay bool   `json:"half_day"`
}

func (h *Handler) listHolidays(w http.ResponseWriter, r *http.Request) error {
	year := h.svc.Today().Year()
	if v := r.URL.Query().Get("year"); v != "" {
		y, err := strconv.Atoi(v)
		if err != nil || y < 2000 || y > 2100 {
			return httpx.Validation(map[string]string{"year": "2000-2100 arası olmalı"})
		}
		year = y
	}
	rows, err := h.svc.pool.Query(r.Context(), `
		SELECT to_char(holiday_date, 'YYYY-MM-DD'), name, half_day FROM public_holidays
		 WHERE extract(year FROM holiday_date) = $1 ORDER BY holiday_date`, year)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[holiday])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// addHoliday registers a holiday. Requests approved earlier keep the cost
// they were booked with; pending ones are recosted at approval.
func (h *Handler) addHoliday(w http.ResponseWriter, r *http.Request) error {
	var in holiday
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	date := parseDate(f, "date", in.Date)
	in.Name = strings.TrimSpace(in.Name)
	if l := len([]rune(in.Name)); l < 1 || l > 100 {
		f["name"] = "1-100 karakter olmalı"
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	err := db.InTx(r.Context(), h.svc.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO public_holidays (holiday_date, name, half_day) VALUES ($1, $2, $3)`,
			date, in.Name, in.HalfDay)
		if db.PgCode(err) == "23505" {
			return httpx.NewError(http.StatusConflict, "holiday_exists", "bu tarihte bir tatil zaten tanımlı")
		}
		if err != nil {
			return err
		}
		a := actor(r)
		return audit.Write(r.Context(), tx, audit.Entry{
			ActorUserID: &a.UserID, ActorRole: a.Role, Action: "leave.holiday_add", EntityType: "public_holiday",
			EntityID: in.Date, New: in, RequestID: a.RequestID, IP: a.IP,
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, in)
	return nil
}

// ---- balance and ledger ----

func (h *Handler) visibleEmployee(r *http.Request) (int64, error) {
	id, err := pathID(r)
	if err != nil {
		return 0, err
	}
	return id, org.CheckVisible(r.Context(), h.svc.pool, principal(r), org.LeaveData, id)
}

func (h *Handler) balance(w http.ResponseWriter, r *http.Request) error {
	id, err := h.visibleEmployee(r)
	if err != nil {
		return err
	}
	b, err := h.svc.GetBalance(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, b)
	return nil
}

type ledgerRow struct {
	ID               int64   `json:"id"`
	Kind             string  `json:"kind"`
	Days             float64 `json:"days"`
	EffectiveDate    string  `json:"effective_date"`
	LeaveRequestID   *int64  `json:"leave_request_id"`
	RelatedRequestID *int64  `json:"related_request_id"`
	Note             string  `json:"note"`
	RunningBalance   float64 `json:"running_balance"`
}

// ledger lists every balance movement with a running balance, oldest first.
func (h *Handler) ledger(w http.ResponseWriter, r *http.Request) error {
	id, err := h.visibleEmployee(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.GetBalance(r.Context(), id); err != nil { // books due accruals
		return err
	}
	rows, err := h.svc.pool.Query(r.Context(), `
		SELECT id, kind, half_days, to_char(effective_date, 'YYYY-MM-DD'), leave_request_id, related_request_id, note,
		       sum(half_days) OVER (ORDER BY effective_date, id)
		  FROM leave_ledger WHERE employee_id = $1 ORDER BY effective_date, id`, id)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ledgerRow, error) {
		var l ledgerRow
		var half, running int
		err := row.Scan(&l.ID, &l.Kind, &half, &l.EffectiveDate, &l.LeaveRequestID, &l.RelatedRequestID, &l.Note, &running)
		l.Days, l.RunningBalance = Days(half), Days(running)
		return l, err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// ---- requests ----

// list: caller's scope, newest first, keyset by id. Filters: status,
// employee_id, for_approval=true (pending requests the caller may decide).
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	scope := org.ScopeFor(p, org.LeaveData)
	if scope.Empty() {
		return httpx.ErrForbidden
	}
	q := r.URL.Query()
	limit, err := httpx.PageLimit(r, 50, 200)
	if err != nil {
		return err
	}
	var cursor, empID *int64
	for name, dst := range map[string]**int64{"cursor": &cursor, "employee_id": &empID} {
		if v := q.Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1 {
				return httpx.Validation(map[string]string{name: "pozitif tam sayı olmalı"})
			}
			*dst = &n
		}
	}
	status := q.Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "cancelled" {
		return httpx.Validation(map[string]string{"status": "pending, approved, rejected veya cancelled"})
	}
	forApproval := q.Get("for_approval") == "true"
	approveAll := p.Can(auth.PermLeaveManage)
	approvePaths := []string{}
	if p.Can(auth.PermLeaveApproveTeam) {
		approvePaths = p.TeamPaths
	}

	args := append(scope.Args(), cursor, empID, status, forApproval, approveAll, approvePaths, p.EmployeeID, limit+1)
	rows, err := h.svc.pool.Query(r.Context(), requestSelect+`
		 WHERE `+org.ScopeSQL+`
		   AND ($4::bigint IS NULL OR r.id < $4)
		   AND ($5::bigint IS NULL OR r.employee_id = $5)
		   AND ($6 = '' OR r.status = $6)
		   AND (NOT $7 OR (r.status = 'pending' AND r.employee_id IS DISTINCT FROM $10
		                   AND ($8 OR u.path <@ ANY($9::ltree[]))))
		 ORDER BY r.id DESC
		 LIMIT $11`, args...)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Request, error) { return scanRequest(row) })
	if err != nil {
		return err
	}
	page := httpx.Page[Request]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = strconv.FormatInt(items[limit-1].ID, 10)
	}
	httpx.WriteJSON(w, http.StatusOK, page)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	req, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return mapErr(err)
	}
	if err := org.CheckVisible(r.Context(), h.svc.pool, principal(r), org.LeaveData, req.EmployeeID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, req)
	return nil
}

type createInput struct {
	EmployeeID *int64  `json:"employee_id"`
	Type       string  `json:"leave_type"`
	StartDate  string  `json:"start_date"`
	EndDate    string  `json:"end_date"`
	HalfDay    HalfDay `json:"half_day"`
	Reason     string  `json:"reason"`
}

// create: employees file for themselves (from today; sick reports up to 30
// days back); HR (leave.manage) may file for anyone and for any date.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	var in createInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	manage := p.Can(auth.PermLeaveManage)
	if in.EmployeeID == nil {
		in.EmployeeID = p.EmployeeID
	}
	self := in.EmployeeID != nil && p.EmployeeID != nil && *in.EmployeeID == *p.EmployeeID
	if in.EmployeeID == nil || (!manage && !(self && p.Can(auth.PermLeaveRequestSelf))) {
		return httpx.ErrForbidden
	}

	f := map[string]string{}
	if !validTypes[in.Type] {
		f["leave_type"] = "annual, sick, unpaid veya excuse"
	}
	start := parseDate(f, "start_date", in.StartDate)
	end := parseDate(f, "end_date", in.EndDate)
	if in.HalfDay != FullDay && in.HalfDay != Morning && in.HalfDay != Afternoon {
		f["half_day"] = `"", "morning" veya "afternoon"`
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len([]rune(in.Reason)) > 500 {
		f["reason"] = "en fazla 500 karakter"
	}
	if len(f) == 0 {
		today := h.svc.Today()
		switch {
		case end.Before(start):
			f["end_date"] = "başlangıçtan önce olamaz"
		case end.Sub(start) > 366*24*time.Hour:
			f["end_date"] = "talep en fazla bir yıl kapsayabilir"
		case in.HalfDay != FullDay && !start.Equal(end):
			f["half_day"] = "yarım gün izin tek bir gün için olmalı"
		case !manage && in.Type != TypeSick && start.Before(today):
			f["start_date"] = "geçmiş tarihli izin talebi oluşturulamaz"
		case !manage && in.Type == TypeSick && start.Before(today.AddDate(0, 0, -30)):
			f["start_date"] = "rapor en fazla 30 gün geriye girilebilir"
		}
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	if err := h.checkEmployed(r.Context(), *in.EmployeeID, start, end); err != nil {
		return err
	}

	req, err := h.svc.Create(r.Context(), actor(r), NewRequest{
		EmployeeID: *in.EmployeeID, Type: in.Type, Start: start, End: end, Half: in.HalfDay, Reason: in.Reason,
	})
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, req)
	return nil
}

func (h *Handler) checkEmployed(ctx context.Context, id int64, start, end time.Time) error {
	var hire time.Time
	var term *time.Time
	err := h.svc.pool.QueryRow(ctx, `SELECT hire_date, termination_date FROM employees WHERE id = $1`, id).Scan(&hire, &term)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Validation(map[string]string{"employee_id": "çalışan bulunamadı"})
	}
	if err != nil {
		return err
	}
	if start.Before(hire) || (term != nil && end.After(*term)) {
		return httpx.Validation(map[string]string{"start_date": "izin, çalışanın işe giriş ve çıkış tarihleri arasında olmalı"})
	}
	return nil
}

type decisionInput struct {
	Note string `json:"note"`
}

func (h *Handler) decision(w http.ResponseWriter, r *http.Request) (int64, string, error) {
	id, err := pathID(r)
	if err != nil {
		return 0, "", err
	}
	var in decisionInput
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return 0, "", err
		}
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 500 {
		return 0, "", httpx.Validation(map[string]string{"note": "en fazla 500 karakter"})
	}
	return id, in.Note, nil
}

// canDecide: HR for anyone, a manager or active delegate for their
// sub-tree; nobody for their own request.
func (h *Handler) canDecide(r *http.Request) func(Request) error {
	p := principal(r)
	return func(req Request) error {
		if p.EmployeeID != nil && *p.EmployeeID == req.EmployeeID {
			return errSelfDecision
		}
		if p.Can(auth.PermLeaveManage) {
			return nil
		}
		if p.Can(auth.PermLeaveApproveTeam) {
			in, err := org.InTeam(r.Context(), h.svc.pool, p, req.EmployeeID)
			if err != nil {
				return err
			}
			if in {
				return nil
			}
		}
		return httpx.ErrForbidden
	}
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) error {
	id, note, err := h.decision(w, r)
	if err != nil {
		return err
	}
	req, err := h.svc.Approve(r.Context(), actor(r), id, note, h.canDecide(r))
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, req)
	return nil
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) error {
	id, note, err := h.decision(w, r)
	if err != nil {
		return err
	}
	req, err := h.svc.Reject(r.Context(), actor(r), id, note, h.canDecide(r))
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, req)
	return nil
}

// cancel: the employee withdraws their own pending request; HR may also
// cancel approved leave, which reverses the charged days.
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p := principal(r)
	req, err := h.svc.Cancel(r.Context(), actor(r), id, func(req Request) error {
		if p.Can(auth.PermLeaveManage) {
			return nil
		}
		own := p.EmployeeID != nil && *p.EmployeeID == req.EmployeeID
		if !own {
			return httpx.ErrForbidden
		}
		if req.Status != "pending" {
			return errNotPending
		}
		return nil
	})
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, req)
	return nil
}
