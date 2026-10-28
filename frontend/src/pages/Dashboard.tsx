import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useAuth } from "../auth";
import { date, dateTime, days, roleNames, time } from "../format";
import { Async, Badge, Card, PageHeader, Stat } from "../ui";
import type { Balance, Page, Period, SystemStatus } from "../types";

export default function DashboardPage() {
  const { me, can } = useAuth();
  const status = useQuery({
    queryKey: ["system-status"],
    queryFn: () => api.get<SystemStatus>("/system/status"),
    refetchInterval: 10_000,
  });
  const balance = useQuery({
    queryKey: ["balance", me?.employee_id],
    queryFn: () => api.get<Balance>(`/leave/employees/${me!.employee_id}/balance`),
    enabled: !!me?.employee_id,
  });
  const approver = can("leave.approve.team") || can("leave.manage");
  const leaveQueue = useQuery({
    queryKey: ["leave-queue-count"],
    queryFn: () => api.get<Page<unknown>>("/leave/requests?for_approval=true&limit=200"),
    enabled: approver,
  });
  const tsApprover = can("timesheet.approve.team") || can("timesheet.manage");
  const tsQueue = useQuery({
    queryKey: ["ts-queue-count"],
    queryFn: () => api.get<Page<unknown>>("/timesheet/corrections?for_approval=true&limit=200"),
    enabled: tsApprover,
  });
  const periods = useQuery({
    queryKey: ["periods"],
    queryFn: () => api.get<{ items: Period[] }>("/payroll/periods"),
    enabled: can("payroll.read.all"),
  });
  const count = (n: number) => (n >= 200 ? "200+" : String(n));

  return (
    <>
      <PageHeader title={`Hoş geldiniz`} sub={`${me?.email} · ${roleNames[me?.role ?? ""]}`} />
      <div className="grid">
        <Card title="Sistem durumu" actions={<span className="live-dot" title="10 sn'de bir yenilenir" />}>
          <Async q={status}>
            {(s) => (
              <div className="stats">
                <Stat label="Aktif çalışan" value={s.active_employees.toLocaleString("tr-TR")} />
                <Stat label="Bugünkü basım" value={s.punches_today.toLocaleString("tr-TR")} hint="simülatör canlı üretir" />
                <Stat label="Bekleyen izin" value={s.pending_leave_requests} />
                <Stat label="Açık basım önerisi" value={s.open_punch_proposals.toLocaleString("tr-TR")} />
                {s.simulator ? (
                  <Stat
                    label="Simülatör"
                    value={s.simulator.last_tick_at ? time(s.simulator.last_tick_at) : "—"}
                    hint={`son çalışma · tohum ${s.simulator.seed} · ${s.simulator.live_punches.toLocaleString("tr-TR")} canlı basım`}
                  />
                ) : (
                  <Stat label="Simülatör" value="yükleniyor" />
                )}
              </div>
            )}
          </Async>
        </Card>

        {me?.employee_id && (
          <Card title="Yıllık izin" actions={<Link to="/leave">Ayrıntı →</Link>}>
            <Async q={balance}>
              {(b) => (
                <div className="stats">
                  <Stat label="Kullanılabilir" value={days(b.available)} />
                  <Stat label="Bakiye" value={days(b.balance)} hint={b.pending ? `${days(b.pending)} bekleyen` : undefined} />
                  <Stat label="Devreden" value={days(b.carried_over)} />
                  <Stat
                    label="Sonraki hakediş"
                    value={b.next_accrual ? days(b.next_accrual.days) : "—"}
                    hint={date(b.next_accrual?.date)}
                  />
                </div>
              )}
            </Async>
          </Card>
        )}

        {(approver || tsApprover) && (
          <Card title="Onayımı bekleyenler">
            <div className="stats">
              {approver && (
                <Link to="/leave?tab=approvals" className="stat-link">
                  <Async q={leaveQueue}>{(p) => <Stat label="İzin talebi" value={count(p.items.length)} />}</Async>
                </Link>
              )}
              {tsApprover && (
                <Link to="/timesheet?tab=corrections" className="stat-link">
                  <Async q={tsQueue}>{(p) => <Stat label="Basım düzeltmesi" value={count(p.items.length)} />}</Async>
                </Link>
              )}
            </div>
          </Card>
        )}

        {can("payroll.read.all") && (
          <Card title="Bordro dönemleri" actions={<Link to="/payroll">Tümü →</Link>}>
            <Async q={periods} isEmpty={(d) => d.items.length === 0} empty="Henüz bordro dönemi açılmadı.">
              {(d) => (
                <ul className="plain">
                  {d.items.slice(0, 5).map((p) => (
                    <li key={p.period}>
                      <strong>{p.period}</strong> <Badge status={p.status} /> <span className="muted">{p.employee_count} kişi · {dateTime(p.last_run_at)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </Async>
          </Card>
        )}
      </div>
    </>
  );
}
