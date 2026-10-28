import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../auth";
import { Async, Card, PageHeader } from "../ui";
import type { Unit } from "../types";
import { useUnits } from "./Employees";

function Node({ unit, mine, children }: { unit: Unit; mine: boolean; children: ReactNode }) {
  return (
    <li>
      <div className={`tree-node ${mine ? "mine" : ""}`}>
        <strong>{unit.name}</strong>
        <span className="muted"> · {unit.employee_count} kişi</span>
        {unit.manager_name && (
          <span className="muted">
            {" "}· Yönetici: <Link to={`/employees/${unit.manager_id}`}>{unit.manager_name}</Link>
          </span>
        )}
        {mine && <span className="badge badge-review">Ekibim</span>}
      </div>
      {children}
    </li>
  );
}

export default function OrgPage() {
  const { me } = useAuth();
  const units = useUnits();
  const team = me?.team_paths ?? [];
  return (
    <>
      <PageHeader title="Organizasyon" sub="Birimler ltree yollarıyla tutulur; yönetici, biriminin altındaki tüm ağacı görür." />
      <Card>
        <Async q={units} isEmpty={(d) => d.items.length === 0}>
          {(d) => {
            const kids = new Map<number | null, Unit[]>();
            d.items.forEach((u) => kids.set(u.parent_id, [...(kids.get(u.parent_id) ?? []), u]));
            const build = (u: Unit): ReactNode => {
              const sub = kids.get(u.id) ?? [];
              return (
                <Node key={u.id} unit={u} mine={team.some((p) => u.path === p || u.path.startsWith(p + "."))}>
                  {sub.length > 0 && <ul>{sub.map(build)}</ul>}
                </Node>
              );
            };
            return <ul className="tree">{(kids.get(null) ?? []).map(build)}</ul>;
          }}
        </Async>
      </Card>
    </>
  );
}
