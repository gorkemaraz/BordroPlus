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
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Routes(r chi.Router) {
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
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	}
	return err
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
