package auth

import "slices"

// Role is a user's single job role. The values match the users.role CHECK.
type Role string

const (
	RoleEmployee   Role = "employee"
	RoleManager    Role = "manager"
	RoleHR         Role = "hr"
	RolePayroll    Role = "payroll"
	RoleAccounting Role = "accounting"
	RoleAuditor    Role = "auditor"
)

// Roles lists every role in display order.
var Roles = []Role{RoleEmployee, RoleManager, RoleHR, RolePayroll, RoleAccounting, RoleAuditor}

// Permission names one capability. Handlers check permissions, never roles,
// so the matrix below is the only place that maps jobs to capabilities.
//
// Suffixes give the data scope: .self = own records, .team = the manager's
// org sub-tree, .all = the whole company.
type Permission string

const (
	PermEmployeeReadSelf Permission = "employee.read.self"
	PermEmployeeReadTeam Permission = "employee.read.team"
	PermEmployeeReadAll  Permission = "employee.read.all"
	PermEmployeeWrite    Permission = "employee.write"
	PermOrgRead          Permission = "org.read"
	PermOrgWrite         Permission = "org.write"

	PermLeaveRequestSelf Permission = "leave.request.self"
	PermLeaveReadTeam    Permission = "leave.read.team"
	PermLeaveApproveTeam Permission = "leave.approve.team"
	PermLeaveReadAll     Permission = "leave.read.all"
	PermLeaveManage      Permission = "leave.manage"

	PermTimesheetReadSelf    Permission = "timesheet.read.self"
	PermTimesheetReadTeam    Permission = "timesheet.read.team"
	PermTimesheetApproveTeam Permission = "timesheet.approve.team"
	PermTimesheetReadAll     Permission = "timesheet.read.all"
	PermTimesheetManage      Permission = "timesheet.manage"

	PermSalaryRead  Permission = "salary.read"
	PermSalaryWrite Permission = "salary.write"

	PermPayrollReadSelf    Permission = "payroll.read.self"
	PermPayrollReadAll     Permission = "payroll.read.all"
	PermPayrollCalculate   Permission = "payroll.calculate"
	PermPayrollApprove     Permission = "payroll.approve"
	PermPayrollClose       Permission = "payroll.close"
	PermPayrollItemsManage Permission = "payroll.items.manage"

	PermReportsRead Permission = "reports.read"
	PermAuditRead   Permission = "audit.read"
	PermUsersManage Permission = "users.manage"
)

// selfService is what every account linked to an employee may do for itself.
var selfService = []Permission{
	PermEmployeeReadSelf, PermOrgRead, PermLeaveRequestSelf, PermTimesheetReadSelf, PermPayrollReadSelf,
}

// rolePermissions is the RBAC matrix. Separation of duties: payroll
// specialists calculate, accounting approves and closes; nobody does both.
// Auditors read everything except plain-text salaries and change nothing.
var rolePermissions = map[Role][]Permission{
	RoleEmployee: selfService,
	RoleManager: concat(selfService,
		PermEmployeeReadTeam, PermLeaveReadTeam, PermLeaveApproveTeam,
		PermTimesheetReadTeam, PermTimesheetApproveTeam),
	RoleHR: concat(selfService,
		PermEmployeeReadAll, PermEmployeeWrite, PermOrgWrite,
		PermLeaveReadAll, PermLeaveManage, PermTimesheetReadAll, PermTimesheetManage,
		PermSalaryRead, PermSalaryWrite, PermReportsRead, PermUsersManage),
	RolePayroll: concat(selfService,
		PermEmployeeReadAll, PermLeaveReadAll, PermTimesheetReadAll,
		PermSalaryRead, PermPayrollReadAll, PermPayrollCalculate, PermPayrollItemsManage, PermReportsRead),
	RoleAccounting: concat(selfService,
		PermEmployeeReadAll, PermPayrollReadAll, PermPayrollApprove, PermPayrollClose, PermReportsRead),
	RoleAuditor: concat(selfService,
		PermEmployeeReadAll, PermLeaveReadAll, PermTimesheetReadAll, PermPayrollReadAll,
		PermReportsRead, PermAuditRead),
}

var permissionSets = func() map[Role]map[Permission]struct{} {
	m := make(map[Role]map[Permission]struct{}, len(rolePermissions))
	for role, perms := range rolePermissions {
		set := make(map[Permission]struct{}, len(perms))
		for _, p := range perms {
			set[p] = struct{}{}
		}
		m[role] = set
	}
	return m
}()

func concat(base []Permission, extra ...Permission) []Permission {
	return append(slices.Clone(base), extra...)
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	_, ok := rolePermissions[r]
	return ok
}

// Can reports whether the role grants p. Unknown roles grant nothing.
func (r Role) Can(p Permission) bool {
	_, ok := permissionSets[r][p]
	return ok
}

// Permissions returns the role's permissions sorted, for the /auth/me response
// and for the frontend to decide which screens to show.
func (r Role) Permissions() []Permission {
	out := slices.Clone(rolePermissions[r])
	slices.Sort(out)
	return slices.Compact(out)
}
