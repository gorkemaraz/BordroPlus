package org

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/db"
	"bordroplus/internal/httpx"
)

// Unit is an org unit as returned by the API. The tree is a flat list with
// parent ids and ltree paths; clients nest it.
type Unit struct {
	ID            int64   `json:"id"`
	ParentID      *int64  `json:"parent_id"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Depth         int     `json:"depth"`
	ManagerID     *int64  `json:"manager_id"`
	ManagerName   *string `json:"manager_name"`
	EmployeeCount int     `json:"employee_count"`
}

var unitCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func (h *Handler) listUnits(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.pool.Query(r.Context(), `
		SELECT u.id, u.parent_id, u.code, u.name, u.path::text, nlevel(u.path), u.manager_id,
		       m.first_name || ' ' || m.last_name,
		       (SELECT count(*) FROM employees e WHERE e.org_unit_id = u.id AND e.termination_date IS NULL)
		  FROM org_units u
		  LEFT JOIN employees m ON m.id = u.manager_id
		 ORDER BY u.path`)
	if err != nil {
		return err
	}
	units, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Unit, error) {
		var u Unit
		err := row.Scan(&u.ID, &u.ParentID, &u.Code, &u.Name, &u.Path, &u.Depth, &u.ManagerID, &u.ManagerName, &u.EmployeeCount)
		return u, err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": units})
	return nil
}

type unitInput struct {
	ParentID  *int64  `json:"parent_id"`
	Code      *string `json:"code"`
	Name      *string `json:"name"`
	ManagerID *int64  `json:"manager_id"`
}

func (in *unitInput) validate(create bool) error {
	f := map[string]string{}
	if in.Code != nil {
		*in.Code = strings.TrimSpace(*in.Code)
		if !unitCode.MatchString(*in.Code) {
			f["code"] = "küçük harf, rakam ve _ (en fazla 63 karakter), harfle başlamalı"
		}
	} else if create {
		f["code"] = "zorunlu"
	}
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
		if l := len([]rune(*in.Name)); l < 1 || l > 200 {
			f["name"] = "1-200 karakter olmalı"
		}
	} else if create {
		f["name"] = "zorunlu"
	}
	if create && in.ParentID == nil {
		f["parent_id"] = "zorunlu (tek kök birim vardır)"
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	return nil
}

// unitWriteError maps constraint violations raised by the tree triggers.
func unitWriteError(err error) error {
	switch db.PgCode(err) {
	case "23505":
		return errConflict("unit_code_taken", "bu birim kodu kullanılıyor")
	case "23503":
		return httpx.Validation(map[string]string{"parent_id": "üst birim veya yönetici bulunamadı"})
	case "23514":
		return httpx.Validation(map[string]string{"parent_id": "birim kendi alt ağacına taşınamaz"})
	}
	return err
}

func (h *Handler) createUnit(w http.ResponseWriter, r *http.Request) error {
	var in unitInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(true); err != nil {
		return err
	}
	var u Unit
	err := db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO org_units (parent_id, code, name, manager_id) VALUES ($1, $2, $3, $4)
			RETURNING id, parent_id, code, name, path::text, nlevel(path), manager_id`,
			in.ParentID, *in.Code, *in.Name, in.ManagerID).
			Scan(&u.ID, &u.ParentID, &u.Code, &u.Name, &u.Path, &u.Depth, &u.ManagerID)
		if err != nil {
			return unitWriteError(err)
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "org_unit.create", "org_unit", u.ID, nil, u))
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, u)
	return nil
}

// updateUnit renames, moves (parent_id) or sets the manager of a unit.
// Moving rewrites the paths of the whole sub-tree in the same transaction.
func (h *Handler) updateUnit(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in unitInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(false); err != nil {
		return err
	}
	var before, after Unit
	err = db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		const cols = `id, parent_id, code, name, path::text, nlevel(path), manager_id`
		err := tx.QueryRow(r.Context(), `SELECT `+cols+` FROM org_units WHERE id = $1 FOR UPDATE`, id).
			Scan(&before.ID, &before.ParentID, &before.Code, &before.Name, &before.Path, &before.Depth, &before.ManagerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.ParentID != nil && before.ParentID == nil {
			return httpx.Validation(map[string]string{"parent_id": "kök birim taşınamaz"})
		}
		err = tx.QueryRow(r.Context(), `
			UPDATE org_units SET
			       parent_id  = COALESCE($2, parent_id),
			       code       = COALESCE($3, code),
			       name       = COALESCE($4, name),
			       manager_id = COALESCE($5, manager_id)
			 WHERE id = $1
			RETURNING `+cols, id, in.ParentID, in.Code, in.Name, in.ManagerID).
			Scan(&after.ID, &after.ParentID, &after.Code, &after.Name, &after.Path, &after.Depth, &after.ManagerID)
		if err != nil {
			return unitWriteError(err)
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "org_unit.update", "org_unit", id, before, after))
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, after)
	return nil
}
