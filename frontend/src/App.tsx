import { useState, type ReactNode } from "react";
import { BrowserRouter, Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom";
import { useAuth } from "./auth";
import { roleNames } from "./format";
import { Loading } from "./ui";
import LoginPage from "./pages/Login";
import DashboardPage from "./pages/Dashboard";
import EmployeesPage from "./pages/Employees";
import EmployeeDetailPage from "./pages/EmployeeDetail";
import OrgPage from "./pages/Org";
import LeavePage from "./pages/Leave";
import TimesheetPage from "./pages/Timesheet";
import PayrollPage from "./pages/Payroll";
import PayslipsPage from "./pages/Payslips";
import ReportsPage from "./pages/Reports";
import AuditPage from "./pages/Audit";

// Navigation entries; a user sees an entry if they hold any of its permissions.
const nav: { to: string; label: string; any: string[] }[] = [
  { to: "/", label: "Panel", any: [] },
  { to: "/employees", label: "Personel", any: ["employee.read.team", "employee.read.all"] },
  { to: "/org", label: "Organizasyon", any: ["org.read"] },
  { to: "/leave", label: "İzinler", any: ["leave.request.self", "leave.read.team", "leave.read.all"] },
  { to: "/timesheet", label: "Puantaj", any: ["timesheet.read.self", "timesheet.read.team", "timesheet.read.all"] },
  { to: "/payroll", label: "Bordro", any: ["payroll.read.all"] },
  { to: "/payslips", label: "Bordrolarım", any: ["payroll.read.self"] },
  { to: "/reports", label: "Raporlar", any: ["reports.read"] },
  { to: "/audit", label: "Denetim kaydı", any: ["audit.read"] },
];

function Guard({ any, children }: { any: string[]; children: ReactNode }) {
  const { can } = useAuth();
  if (any.length && !any.some(can)) {
    return (
      <div className="state error-box" role="alert">
        <strong>Bu sayfayı görme yetkiniz yok.</strong>
      </div>
    );
  }
  return <>{children}</>;
}

function Shell() {
  const { me, can, logout } = useAuth();
  const [open, setOpen] = useState(false);
  const loc = useLocation();
  const items = nav.filter((n) => n.any.length === 0 || n.any.some(can));
  const guard = (path: string) => nav.find((n) => n.to === path)?.any ?? [];

  return (
    <div className="shell">
      <header className="topbar">
        <button className="menu-btn" aria-label="Menü" aria-expanded={open} onClick={() => setOpen(!open)}>
          ☰
        </button>
        <span className="brand">BordroPlus</span>
        <span className="who">
          <span className="who-email">{me?.email}</span> · {roleNames[me?.role ?? ""]}
          {me && me.team_paths.length > 0 && me.role !== "manager" && <span className="badge badge-review">Vekil</span>}
        </span>
        <button className="btn small ghost" onClick={() => logout()}>
          Çıkış
        </button>
      </header>
      <nav className={`sidenav ${open ? "open" : ""}`} onClick={() => setOpen(false)}>
        {items.map((n) => (
          <NavLink key={n.to} to={n.to} end={n.to === "/"}>
            {n.label}
          </NavLink>
        ))}
      </nav>
      <main className="content" key={loc.pathname}>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/employees" element={<Guard any={guard("/employees")}><EmployeesPage /></Guard>} />
          <Route path="/employees/:id" element={<EmployeeDetailPage />} />
          <Route path="/org" element={<Guard any={guard("/org")}><OrgPage /></Guard>} />
          <Route path="/leave" element={<Guard any={guard("/leave")}><LeavePage /></Guard>} />
          <Route path="/timesheet" element={<Guard any={guard("/timesheet")}><TimesheetPage /></Guard>} />
          <Route path="/payroll" element={<Guard any={guard("/payroll")}><PayrollPage /></Guard>} />
          <Route path="/payslips" element={<Guard any={guard("/payslips")}><PayslipsPage /></Guard>} />
          <Route path="/reports" element={<Guard any={guard("/reports")}><ReportsPage /></Guard>} />
          <Route path="/audit" element={<Guard any={guard("/audit")}><AuditPage /></Guard>} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

export default function App() {
  const { me, ready } = useAuth();
  if (!ready) return <Loading label="Oturum kontrol ediliyor…" />;
  return <BrowserRouter>{me ? <Shell /> : <LoginPage />}</BrowserRouter>;
}
