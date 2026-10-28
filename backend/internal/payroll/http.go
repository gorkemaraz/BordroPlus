package payroll

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"bordroplus/internal/auth"
	"bordroplus/internal/httpx"
)

// Handler serves /payroll.
type Handler struct {
	svc     *Service
	workers int // parallel calculations in a period run
}

func NewHandler(svc *Service, workers int) *Handler {
	return &Handler{svc: svc, workers: max(1, workers)}
}

func (h *Handler) Routes(r chi.Router) {
	need := auth.Require
	r.With(need(auth.PermPayrollReadAll)).Get("/payroll/periods", httpx.Handle(h.listPeriods))
	r.With(need(auth.PermPayrollReadAll)).Get("/payroll/periods/{period}", httpx.Handle(h.getPeriod))
	r.With(need(auth.PermPayrollCalculate)).Post("/payroll/periods/{period}/run", httpx.Handle(h.run))
	r.With(need(auth.PermPayrollCalculate)).Post("/payroll/periods/{period}/submit", h.transition("submit"))
	r.With(need(auth.PermPayrollApprove)).Post("/payroll/periods/{period}/approve", h.transition("approve"))
	r.With(need(auth.PermPayrollApprove)).Post("/payroll/periods/{period}/reject", h.transition("reject"))
	r.With(need(auth.PermPayrollClose)).Post("/payroll/periods/{period}/close", h.transition("close"))

	r.With(auth.Require(auth.PermPayrollReadAll)).Get("/payroll/rule-sets", httpx.Handle(h.ruleSets))
	r.With(auth.Require(auth.PermPayrollCalculate)).Post("/payroll/employees/{id}/calculate", httpx.Handle(h.calculate))
	r.With(auth.Require(auth.PermPayrollCalculate)).Post("/payroll/employees/{id}/recalculate", httpx.Handle(h.recalculate))
	r.Get("/payroll/employees/{id}/results", httpx.Handle(h.results))
	r.Get("/payroll/employees/{id}/results/{period}", httpx.Handle(h.result))
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		return 0, httpx.ErrNotFound
	}
	return id, nil
}

func parsePeriod(field, v string) (time.Time, error) {
	t, err := time.Parse("2006-01", v)
	if err != nil {
		return time.Time{}, httpx.Validation(map[string]string{field: "YYYY-AA biçiminde olmalı"})
	}
	return t, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, ErrNoRuleSet):
		return httpx.NewError(http.StatusUnprocessableEntity, "no_rule_set", "bu dönem için bordro parametresi tanımlı değil")
	case errors.Is(err, ErrNotEmployed):
		return httpx.NewError(http.StatusUnprocessableEntity, "not_employed", "çalışan bu dönemde çalışmıyor")
	case errors.Is(err, ErrNoSalary):
		return httpx.NewError(http.StatusUnprocessableEntity, "no_salary", "dönem için geçerli maaş kaydı yok")
	case errors.Is(err, ErrPeriodFrozen):
		return httpx.NewError(http.StatusConflict, "period_frozen", "bordro dönemi kontrol, onay ya da kapanış aşamasında; değiştirilemez")
	case errors.Is(err, ErrInvalidTransition):
		return httpx.NewError(http.StatusConflict, "invalid_transition", "dönem bu işlem için uygun durumda değil")
	case errors.Is(err, ErrPreviousOpen):
		return httpx.NewError(http.StatusConflict, "previous_period_open", "önceki bordro dönemi kapatılmadan bu dönem ilerletilemez")
	case errors.Is(err, ErrNotRun):
		return httpx.NewError(http.StatusConflict, "not_calculated", "dönem hatasız hesaplanmadan kontrole gönderilemez")
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	}
	return err
}

func actor(r *http.Request) Actor {
	p, _ := auth.FromContext(r.Context())
	return Actor{UserID: p.UserID, Role: string(p.Role), RequestID: httpx.RequestID(r.Context()), IP: httpx.ClientIP(r)}
}

func (h *Handler) listPeriods(w http.ResponseWriter, r *http.Request) error {
	items, err := h.svc.ListPeriods(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (h *Handler) getPeriod(w http.ResponseWriter, r *http.Request) error {
	period, err := parsePeriod("period", chi.URLParam(r, "period"))
	if err != nil {
		return err
	}
	p, err := h.svc.GetPeriod(r.Context(), period)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, p)
	return nil
}

// run calculates the whole period in parallel and reports duration,
// failures and the period digest.
func (h *Handler) run(w http.ResponseWriter, r *http.Request) error {
	period, err := parsePeriod("period", chi.URLParam(r, "period"))
	if err != nil {
		return err
	}
	rep, err := h.svc.RunPeriod(r.Context(), actor(r), period, h.workers)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
	return nil
}

func (h *Handler) transition(action string) http.HandlerFunc {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		period, err := parsePeriod("period", chi.URLParam(r, "period"))
		if err != nil {
			return err
		}
		var in struct {
			Note string `json:"note"`
		}
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(w, r, &in); err != nil {
				return err
			}
		}
		if len([]rune(in.Note)) > 500 {
			return httpx.Validation(map[string]string{"note": "en fazla 500 karakter"})
		}
		if action == "reject" && in.Note == "" {
			return httpx.Validation(map[string]string{"note": "ret gerekçesi zorunlu"})
		}
		p, err := h.svc.Transition(r.Context(), actor(r), period, action, in.Note)
		if err != nil {
			return mapErr(err)
		}
		httpx.WriteJSON(w, http.StatusOK, p)
		return nil
	})
}

// canRead: payroll.read.all, or the employee's own payroll (payroll.read.self).
// Managers never see their team's pay.
func canRead(r *http.Request, employeeID int64) error {
	p, _ := auth.FromContext(r.Context())
	if p.Can(auth.PermPayrollReadAll) {
		return nil
	}
	if p.Can(auth.PermPayrollReadSelf) && p.EmployeeID != nil && *p.EmployeeID == employeeID {
		return nil
	}
	return httpx.ErrForbidden
}

func (h *Handler) ruleSets(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.svc.pool.Query(r.Context(), `SELECT lower(valid_during) FROM payroll_rule_sets ORDER BY lower(valid_during)`)
	if err != nil {
		return err
	}
	starts, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return err
	}
	items := make([]RuleSet, 0, len(starts))
	for _, s := range starts {
		rs, err := RuleSetFor(r.Context(), h.svc.pool, s)
		if err != nil {
			return err
		}
		items = append(items, rs)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

type periodInput struct {
	Period string `json:"period"`
	From   string `json:"from"`
}

func (h *Handler) calculate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in periodInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	period, err := parsePeriod("period", in.Period)
	if err != nil {
		return err
	}
	res, err := h.svc.CalculateMonth(r.Context(), id, period)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, res)
	return nil
}

// recalculate re-runs from a month through the last calculated month of the
// year and reports what changed (retroactive corrections).
func (h *Handler) recalculate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in periodInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	from, err := parsePeriod("from", in.From)
	if err != nil {
		return err
	}
	changes, err := h.svc.RecalculateFrom(r.Context(), id, from)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"changes": changes})
	return nil
}

func (h *Handler) results(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := canRead(r, id); err != nil {
		return err
	}
	year := time.Now().Year()
	if v := r.URL.Query().Get("year"); v != "" {
		if year, err = strconv.Atoi(v); err != nil || year < 2000 || year > 2100 {
			return httpx.Validation(map[string]string{"year": "2000-2100"})
		}
	}
	rows, err := h.svc.pool.Query(r.Context(), `
		SELECT period, result FROM payroll_results
		 WHERE employee_id = $1 AND period >= make_date($2, 1, 1) AND period < make_date($2 + 1, 1, 1) ORDER BY period`, id, year)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var p time.Time
		var raw []byte
		if err := row.Scan(&p, &raw); err != nil {
			return Summary{}, err
		}
		var res Result
		err := json.Unmarshal(raw, &res)
		return summary(p, res), err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (h *Handler) result(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := canRead(r, id); err != nil {
		return err
	}
	period, err := parsePeriod("period", chi.URLParam(r, "period"))
	if err != nil {
		return err
	}
	res, in, err := h.svc.StoredResult(r.Context(), id, period)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"result": res, "inputs": in})
	return nil
}
