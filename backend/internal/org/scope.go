// Package org serves the organisation tree, the employee card, salaries and
// manager delegations, and defines the data scope every module uses to decide
// whose records a caller may see.
package org

import (
	"bordroplus/internal/auth"
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
