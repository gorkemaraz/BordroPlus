import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useAuth } from "../auth";
import { currentMonth, money, month as monthName } from "../format";
import { Async, Card, PageHeader, Table } from "../ui";
import type { PaySummary } from "../types";
import { PayslipLines } from "./Payroll";

export default function PayslipsPage() {
  const { me } = useAuth();
  const id = me!.employee_id!;
  const [year, setYear] = useState(Number(currentMonth().slice(0, 4)));
  const [period, setPeriod] = useState<string | null>(null);
  const list = useQuery({
    queryKey: ["my-results", id, year],
    queryFn: () => api.get<{ items: PaySummary[] }>(`/payroll/employees/${id}/results?year=${year}`),
  });
  return (
    <>
      <PageHeader title="Bordrolarım" sub="Yalnızca onaylanmış ya da kapanmış dönemler görünür." />
      <div className="grid">
        <Card
          title={`${year}`}
          actions={
            <div className="row-actions">
              <button className="btn small ghost" onClick={() => setYear(year - 1)} aria-label="Önceki yıl">←</button>
              <button className="btn small ghost" onClick={() => setYear(year + 1)} aria-label="Sonraki yıl" disabled={year >= Number(currentMonth().slice(0, 4))}>→</button>
            </div>
          }
        >
          <Async q={list} isEmpty={(d) => d.items.length === 0} empty="Bu yıl için kesinleşmiş bordronuz yok.">
            {(d) => (
              <Table
                rows={d.items}
                rowKey={(r) => r.period}
                onRow={(r) => setPeriod(r.period)}
                cols={[
                  { label: "Dönem", cell: (r) => monthName(r.period) },
                  { label: "Brüt", cell: (r) => money(r.gross), num: true },
                  { label: "Gelir vergisi", cell: (r) => money(r.income_tax), num: true },
                  { label: "Net", cell: (r) => <strong>{money(r.net)}</strong>, num: true },
                ]}
              />
            )}
          </Async>
        </Card>
        {period && <PayslipLines employeeId={id} period={period} />}
      </div>
    </>
  );
}
