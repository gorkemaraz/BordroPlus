// Package org serves the organisation tree, the employee card, salaries and
// manager delegations, and defines the data scope every module uses to decide
// whose records a caller may see.
package org

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/httpx"
)

// Scope is the set of employees a caller may see for one kind of data. It is
// applied in SQL, never by filtering rows in Go:
//
//	WHERE ($1::bool OR u.path <@ ANY($2::ltree[]) OR e.id = $3)
//
// with u the employee's org unit. path <@ uses the org_units_path_gist index.
type Scope struct {
	All       bool
	TeamPaths []string
	SelfID    *int64
}

// Family names the three permissions of one data kind.
type Family struct{ All, Team, Self auth.Permission }

var (
	EmployeeData  = Family{auth.PermEmployeeReadAll, auth.PermEmployeeReadTeam, auth.PermEmployeeReadSelf}
	LeaveData     = Family{auth.PermLeaveReadAll, auth.PermLeaveReadTeam, auth.PermLeaveRequestSelf}
	TimesheetData = Family{auth.PermTimesheetReadAll, auth.PermTimesheetReadTeam, auth.PermTimesheetReadSelf}
)

// ScopeFor computes p's scope for a data family.
func ScopeFor(p auth.Principal, f Family) Scope {
	s := Scope{All: p.Can(f.All), TeamPaths: []string{}}
	if p.Can(f.Team) {
		s.TeamPaths = p.TeamPaths
	}
	if p.Can(f.Self) {
		s.SelfID = p.EmployeeID
	}
	return s
}

// Empty reports whether the scope can match nothing.
func (s Scope) Empty() bool { return !s.All && len(s.TeamPaths) == 0 && s.SelfID == nil }

// Args returns the three SQL arguments for the WHERE fragment above.
func (s Scope) Args() []any { return []any{s.All, s.TeamPaths, s.SelfID} }

// CheckVisible returns nil when employee id is inside p's scope for family f,
// httpx.ErrNotFound when it does not exist and httpx.ErrForbidden otherwise.
func CheckVisible(ctx context.Context, q db.Querier, p auth.Principal, f Family, id int64) error {
	var visible bool
	args := append(ScopeFor(p, f).Args(), id)
	err := q.QueryRow(ctx, `
		SELECT `+ScopeSQL+` FROM employees e JOIN org_units u ON u.id = e.org_unit_id WHERE e.id = $4`, args...).Scan(&visible)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return httpx.ErrNotFound
	case err != nil:
		return err
	case !visible:
		return httpx.ErrForbidden
	}
	return nil
}

// InTeam reports whether the employee belongs to one of p's team sub-trees
// (own units or delegated ones).
func InTeam(ctx context.Context, q db.Querier, p auth.Principal, id int64) (bool, error) {
	if len(p.TeamPaths) == 0 {
		return false, nil
	}
	var in bool
	err := q.QueryRow(ctx, `
		SELECT u.path <@ ANY($1::ltree[]) FROM employees e JOIN org_units u ON u.id = e.org_unit_id WHERE e.id = $2`,
		p.TeamPaths, id).Scan(&in)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return in, err
}
