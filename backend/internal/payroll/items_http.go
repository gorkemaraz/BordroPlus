package payroll

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"bordroplus/internal/auth"
	"bordroplus/internal/httpx"
	"bordroplus/internal/money"
)

// payslip: payroll.read.all sees any stored month (drafts are marked);
// employees see their own only once the period is approved or closed.
// ?format=html returns a print-ready page.
func (h *Handler) payslip(w http.ResponseWriter, r *http.Request) error {
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
	p, _ := auth.FromContext(r.Context())
	slip, err := h.svc.GetPayslip(r.Context(), id, period, !p.Can(auth.PermPayrollReadAll))
	if err != nil {
		return mapErr(err)
	}
	if r.URL.Query().Get("format") == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		return slip.WriteHTML(w)
	}
	httpx.WriteJSON(w, http.StatusOK, slip)
	return nil
}

type itemInput struct {
	EmployeeID  int64  `json:"employee_id"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Amount      string `json:"amount"`
	Instalments int    `json:"instalments"`
	StartPeriod string `json:"start_period"`
}

func (h *Handler) createItem(w http.ResponseWriter, r *http.Request) error {
	var in itemInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if in.EmployeeID < 1 {
		f["employee_id"] = "zorunlu"
	}
	if in.Kind != KindBonus && in.Kind != KindAdvance && in.Kind != KindLoan {
		f["kind"] = "bonus, advance veya loan"
	}
	in.Label = strings.TrimSpace(in.Label)
	if l := len([]rune(in.Label)); l < 1 || l > 100 {
		f["label"] = "1-100 karakter"
	}
	amount, err := money.Parse(in.Amount)
	if err != nil || amount <= 0 {
		f["amount"] = "pozitif tutar, en fazla iki ondalık"
	}
	if in.Instalments == 0 {
		in.Instalments = 1
	}
	if in.Instalments < 1 || in.Instalments > 36 || (in.Kind != KindLoan && in.Instalments != 1) {
		f["instalments"] = "borç için 1-36, diğerleri için 1"
	}
	start, err := time.Parse("2006-01", in.StartPeriod)
	if err != nil {
		f["start_period"] = "YYYY-AA"
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	it, err := h.svc.CreateItem(r.Context(), actor(r), NewItem{
		EmployeeID: in.EmployeeID, Kind: in.Kind, Label: in.Label, Amount: amount, Instalments: in.Instalments, Start: start,
	})
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, it)
	return nil
}

func (h *Handler) cancelItem(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	it, err := h.svc.CancelItem(r.Context(), actor(r), id)
	if err != nil {
		return mapErr(err)
	}
	httpx.WriteJSON(w, http.StatusOK, it)
	return nil
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) error {
	var emp *int64
	var period *time.Time
	q := r.URL.Query()
	if v := q.Get("employee_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return httpx.Validation(map[string]string{"employee_id": "pozitif tam sayı"})
		}
		emp = &n
	}
	if v := q.Get("period"); v != "" {
		t, err := parsePeriod("period", v)
		if err != nil {
			return err
		}
		period = &t
	}
	items, err := h.svc.ListItems(r.Context(), emp, period)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}
