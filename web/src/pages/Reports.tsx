import { useState, type ReactNode } from 'react';
import { api } from '../api';
import { AsyncView, Field, Form, PageHead } from '../components/ui';
import { COMP_TR, STATUS_TR, addDays, isoDay } from '../format';
import { useApi } from '../hooks';

interface Report {
  donations: Record<string, number>; tests_positive: number; tests_total: number; produced: Record<string, number>;
  issued: Record<string, number>; expired: Record<string, number>; requests_by_status: Record<string, number>;
  emergency_median_fill_minutes: number | null; ineligible_attempts: number; issued_by_hospital: { hospital: string; units: number }[];
}

const KV = ({ title, rows }: { title: string; rows: [ReactNode, number][] }) => (
  <div className="card"><h3>{title}</h3>
    <div className="table-wrap"><table><tbody>
      {rows.length ? rows.map(([k, v], i) => <tr key={i}><td>{k}</td><td className="num">{v}</td></tr>) : <tr><td>—</td></tr>}
    </tbody></table></div>
  </div>
);

export default function Reports() {
  const [range, setRange] = useState({ from: isoDay(addDays(-30)), to: isoDay(addDays(1)) });
  const rep = useApi(() => api.get<Report>(`/api/v1/reports/summary?from=${range.from}&to=${range.to}`), [range]);
  const comp = (o: Record<string, number>) => Object.entries(o).map(([k, v]) => [COMP_TR[k] ?? k, v] as [string, number]);
  return (
    <>
      <PageHead title="Dağıtım ve raporlar">
        <Form className="inline" submit="Göster" primary={false} onSubmit={async (v) => setRange({ from: v.from, to: v.to })}>
          <Field label="Başlangıç"><input type="date" name="from" defaultValue={range.from} /></Field>
          <Field label="Bitiş"><input type="date" name="to" defaultValue={range.to} /></Field>
        </Form>
      </PageHead>
      <AsyncView state={rep}>
        {(r) => (
          <>
            <div className="grid g3">
              <div className="card stat"><b>{Object.values(r.donations).reduce((a, b) => a + b, 0)}</b><span>Bağış</span></div>
              <div className="card stat"><b>{r.tests_total ? ((100 * r.tests_positive) / r.tests_total).toFixed(2) : 0}%</b><span>Pozitif test oranı ({r.tests_positive}/{r.tests_total})</span></div>
              <div className="card stat"><b>{r.ineligible_attempts}</b><span>Uygunsuz bağış denemesi</span></div>
              <div className="card stat"><b>{r.emergency_median_fill_minutes != null ? `${r.emergency_median_fill_minutes.toFixed(1)} dk` : '—'}</b><span>Çok acil talepte medyan karşılama</span></div>
            </div>
            <div className="grid g3 mt">
              <KV title="Üretilen" rows={comp(r.produced)} />
              <KV title="Dağıtılan" rows={comp(r.issued)} />
              <KV title="SKT kaybı" rows={comp(r.expired)} />
              <KV title="Talepler" rows={Object.entries(r.requests_by_status).map(([k, v]) => [STATUS_TR[k] ?? k, v])} />
              <KV title="Hastanelere dağıtım" rows={r.issued_by_hospital.map((h) => [h.hospital, h.units])} />
            </div>
          </>
        )}
      </AsyncView>
    </>
  );
}
