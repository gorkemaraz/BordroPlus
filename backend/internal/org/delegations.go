package org

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/httpx"
)

// Delegation hands a manager's team scope (viewing and approving leave and
// timesheets of their sub-tree) to a delegate for a date range, inclusive.
type Delegation struct {
	ID                 int64      `json:"id"`
	ManagerEmployeeID  int64      `json:"manager_employee_id"`
	ManagerName        string     `json:"manager_name"`
	DelegateEmployeeID int64      `json:"delegate_employee_id"`
	DelegateName       string     `json:"delegate_name"`
	StartDate          string     `json:"start_date"`
	EndDate            string     `json:"end_date"`
	Reason             string     `json:"reason"`
	RevokedAt          *time.Time `json:"revoked_at"`
	Active             bool       `json:"active"` // covers today and not revoked
}

// upper(valid_during) is exclusive; end_date is shown inclusive.
const delegationSelect = `
	SELECT d.id, d.manager_employee_id, m.first_name || ' ' || m.last_name,
	       d.delegate_employee_id, x.first_name || ' ' || x.last_name,
	       to_char(lower(d.valid_during), 'YYYY-MM-DD'), to_char(upper(d.valid_during) - 1, 'YYYY-MM-DD'),
	       d.reason, d.revoked_at, d.revoked_at IS NULL AND d.valid_during @> current_date
	  FROM manager_delegations d
	  JOIN employees m ON m.id = d.manager_employee_id
	  JOIN employees x ON x.id = d.delegate_employee_id`

func scanDelegation(row pgx.CollectableRow) (Delegation, error) {
	var d Delegation
	err := row.Scan(&d.ID, &d.ManagerEmployeeID, &d.ManagerName, &d.DelegateEmployeeID, &d.DelegateName,
		&d.StartDate, &d.EndDate, &d.Reason, &d.RevokedAt, &d.Active)
	return d, err
}

var (
	errDelegationOverlap = errConflict("delegation_overlap", "bu yöneticinin aynı tarihlerle çakışan bir vekâleti var")
	errNotAManager       = httpx.Validation(map[string]string{"manager_employee_id": "bu çalışan herhangi bir birimin yöneticisi değil"})
)

// listDelegations: HR sees every delegation; everyone else sees the ones
// they gave or received. ?active=true keeps only those in force today.
func (h *Handler) listDelegations(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	all := p.Can(auth.PermDelegationManageAll)
	if !all && p.EmployeeID == nil {
		return httpx.ErrForbidden
	}
	rows, err := h.pool.Query(r.Context(), delegationSelect+`
		 WHERE ($1::bool OR d.manager_employee_id = $2 OR d.delegate_employee_id = $2)
		   AND (NOT $3::bool OR (d.revoked_at IS NULL AND d.valid_during @> current_date))
		 ORDER BY lower(d.valid_during) DESC, d.id DESC
		 LIMIT 500`, all, p.EmployeeID, r.URL.Query().Get("active") == "true")
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, scanDelegation)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

type delegationInput struct {
	ManagerEmployeeID  *int64 `json:"manager_employee_id"`
	DelegateEmployeeID int64  `json:"delegate_employee_id"`
	StartDate          string `json:"start_date"`
	EndDate            string `json:"end_date"`
	Reason             string `json:"reason"`
}

// createDelegation: a manager delegates their own units; HR may set up a
// delegation for any manager. Overlapping delegations of one manager are
// rejected by the exclusion constraint, even under concurrent requests.
func (h *Handler) createDelegation(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	all := p.Can(auth.PermDelegationManageAll)
	if !all && !p.Can(auth.PermDelegationManageSelf) {
		return httpx.ErrForbidden
	}
	var in delegationInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.ManagerEmployeeID == nil {
		in.ManagerEmployeeID = p.EmployeeID
	}
	if !all && (p.EmployeeID == nil || in.ManagerEmployeeID == nil || *in.ManagerEmployeeID != *p.EmployeeID) {
		return httpx.ErrForbidden // managers delegate only their own scope
	}

	f := map[string]string{}
	if in.ManagerEmployeeID == nil {
		f["manager_employee_id"] = "zorunlu"
	}
	if in.DelegateEmployeeID < 1 {
		f["delegate_employee_id"] = "zorunlu"
	} else if in.ManagerEmployeeID != nil && in.DelegateEmployeeID == *in.ManagerEmployeeID {
		f["delegate_employee_id"] = "yönetici kendisine vekâlet veremez"
	}
	start := parseDate(f, "start_date", &in.StartDate)
	end := parseDate(f, "end_date", &in.EndDate)
	if start != nil && end != nil {
		if end.Before(*start) {
			f["end_date"] = "başlangıçtan önce olamaz"
		} else if end.Sub(*start) > 366*24*time.Hour {
			f["end_date"] = "vekâlet en fazla bir yıl sürebilir"
		}
		if end.Before(h.today()) {
			f["end_date"] = "geçmişte biten vekâlet oluşturulamaz"
		}
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len([]rune(in.Reason)) > 500 {
		f["reason"] = "en fazla 500 karakter"
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}

	var id int64
	err := db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var manages, delegateOK bool
		err := tx.QueryRow(r.Context(), `
			SELECT EXISTS (SELECT 1 FROM org_units WHERE manager_id = $1),
			       EXISTS (SELECT 1 FROM employees WHERE id = $2 AND (termination_date IS NULL OR termination_date >= $3))`,
			*in.ManagerEmployeeID, in.DelegateEmployeeID, *end).Scan(&manages, &delegateOK)
		if err != nil {
			return err
		}
		if !manages {
			return errNotAManager
		}
		if !delegateOK {
			return httpx.Validation(map[string]string{"delegate_employee_id": "çalışan bulunamadı ya da vekâlet süresince çalışmıyor"})
		}
		err = tx.QueryRow(r.Context(), `
			INSERT INTO manager_delegations (manager_employee_id, delegate_employee_id, valid_during, reason, created_by)
			VALUES ($1, $2, daterange($3, $4, '[]'), $5, $6) RETURNING id`,
			*in.ManagerEmployeeID, in.DelegateEmployeeID, *start, *end, in.Reason, p.UserID).Scan(&id)
		if db.PgCode(err) == "23P01" {
			return errDelegationOverlap
		}
		if err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "delegation.create", "delegation", id, nil, in))
	})
	if err != nil {
		return err
	}
	d, err := h.delegation(r, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
	return nil
}

// revokeDelegation ends a delegation now. Allowed for HR and the delegating manager.
func (h *Handler) revokeDelegation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p := principal(r)
	err = db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var manager int64
		var revoked bool
		err := tx.QueryRow(r.Context(), `
			SELECT manager_employee_id, revoked_at IS NOT NULL FROM manager_delegations WHERE id = $1 FOR UPDATE`, id).
			Scan(&manager, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		own := p.EmployeeID != nil && *p.EmployeeID == manager && p.Can(auth.PermDelegationManageSelf)
		if !own && !p.Can(auth.PermDelegationManageAll) {
			return httpx.ErrForbidden
		}
		if revoked {
			return errConflict("delegation_revoked", "vekâlet zaten iptal edilmiş")
		}
		if _, err := tx.Exec(r.Context(), `UPDATE manager_delegations SET revoked_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "delegation.revoke", "delegation", id, nil, nil))
	})
	if err != nil {
		return err
	}
	d, err := h.delegation(r, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, d)
	return nil
}

func (h *Handler) delegation(r *http.Request, id int64) (Delegation, error) {
	rows, err := h.pool.Query(r.Context(), delegationSelect+` WHERE d.id = $1`, id)
	if err != nil {
		return Delegation{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanDelegation)
}
