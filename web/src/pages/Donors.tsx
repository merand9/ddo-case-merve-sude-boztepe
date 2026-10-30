import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, type Donor, type Page } from '../api';
import { useAuth } from '../auth';
import { AsyncView, Card, Field, Form, GroupSelect, PageHead, Table } from '../components/ui';
import { fmtDate } from '../format';
import { useApi } from '../hooks';

export default function Donors() {
  const { user } = useAuth();
  const nav = useNavigate();
  const canCreate = user?.role === 'nurse' || user?.role === 'admin';
  const [filter, setFilter] = useState({ q: '', group: '' });
  const [extra, setExtra] = useState<Donor[]>([]);
  const [cursor, setCursor] = useState<number | null>(null);
  const page = useApi(async () => {
    const q = new URLSearchParams({ limit: '25', ...(filter.q && { q: filter.q }), ...(filter.group && { group: filter.group }) });
    const r = await api.get<Page<Donor>>(`/api/v1/donors?${q}`);
    setExtra([]); setCursor(r.next_cursor);
    return r;
  }, [filter]);
  const more = async () => {
    const q = new URLSearchParams({ limit: '25', cursor: String(cursor), ...(filter.q && { q: filter.q }), ...(filter.group && { group: filter.group }) });
    const r = await api.get<Page<Donor>>(`/api/v1/donors?${q}`);
    setExtra((x) => [...x, ...r.items]); setCursor(r.next_cursor);
  };
  return (
    <>
      <PageHead title="Bağışçılar" sub="Ad, soyad ya da bağışçı no ile arayın." />
      <Form className="inline card" submit="Ara" onSubmit={async (v) => setFilter({ q: v.q.trim(), group: v.group })}>
        <Field label="Arama" grow><input name="q" placeholder="ör. Yılmaz ya da D0001234" /></Field>
        <Field label="Kan grubu"><GroupSelect all="Tümü" /></Field>
      </Form>
      <div className="mt">
        <AsyncView state={page} empty="Eşleşen bağışçı yok">
          {(r) => (
            <>
              <Table head={['No', 'Ad soyad', 'Grup', 'Cinsiyet', 'Doğum', '']}>
                {[...r.items, ...extra].map((d) => (
                  <tr key={d.id}>
                    <td className="mono">{d.donor_no}</td><td>{d.first_name} {d.last_name}</td><td className="grp">{d.group}</td>
                    <td>{d.sex === 'F' ? 'K' : 'E'}</td><td>{fmtDate(d.birth_date)}</td>
                    <td>{canCreate && <Link to={`/donors/${d.id}`}>Aç →</Link>}</td>
                  </tr>
                ))}
              </Table>
              {cursor && <button className="mt-s" onClick={more}>Daha fazla</button>}
            </>
          )}
        </AsyncView>
      </div>
      {canCreate && (
        <Card title="Yeni bağışçı kaydı" className="mt">
          <Form submit="Kaydet" onSubmit={async (v) => {
            const split = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean);
            const d = await api.post<Donor>('/api/v1/donors', {
              first_name: v.first_name, last_name: v.last_name, sex: v.sex, birth_date: v.birth_date,
              abo: v.group.slice(0, -1), rh_positive: v.group.endsWith('+'), phone: v.phone,
              health: { medications: split(v.meds), conditions: split(v.conds) },
            });
            nav(`/donors/${d.id}`);
          }}>
            <div className="form-grid">
              <Field label="Ad"><input name="first_name" required maxLength={80} /></Field>
              <Field label="Soyad"><input name="last_name" required maxLength={80} /></Field>
              <Field label="Cinsiyet"><select name="sex"><option value="F">Kadın</option><option value="M">Erkek</option></select></Field>
              <Field label="Doğum tarihi"><input name="birth_date" type="date" required /></Field>
              <Field label="Kan grubu"><GroupSelect /></Field>
              <Field label="Telefon"><input name="phone" /></Field>
              <Field label="Kullandığı ilaçlar (virgülle)"><input name="meds" placeholder="şifreli saklanır" /></Field>
              <Field label="Kronik hastalıklar (virgülle)"><input name="conds" placeholder="şifreli saklanır" /></Field>
            </div>
          </Form>
        </Card>
      )}
    </>
  );
}
