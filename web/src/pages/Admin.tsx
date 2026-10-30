import { useRef, useState } from 'react';
import { api, type Page, type User } from '../api';
import { AsyncView, Card, Field, Form, PageHead, Table } from '../components/ui';
import { ROLE_TR, fmtDate, fmtDT, type Role } from '../format';
import { useApi } from '../hooks';

interface Rule { id: number; valid_from: string; valid_to?: string | null; note?: string; params: unknown }
interface AuditEntry { id: number; at: string; actor_id?: number; actor_role?: Role; action: string; entity: string; entity_id?: string; reason?: string }

export default function Admin() {
  const users = useApi(() => api.get<User[]>('/api/v1/users'));
  const rules = useApi(() => api.get<Rule[]>('/api/v1/rules?code=eligibility'));
  const params = useRef<HTMLTextAreaElement>(null);
  const [action, setAction] = useState('');
  const [extra, setExtra] = useState<AuditEntry[]>([]);
  const [cursor, setCursor] = useState<number | null>(null);
  const audit = useApi(async () => {
    const r = await api.get<Page<AuditEntry>>(`/api/v1/audit?limit=30${action ? `&action=${encodeURIComponent(action)}` : ''}`);
    setExtra([]); setCursor(r.next_cursor);
    return r;
  }, [action]);
  const more = async () => {
    const r = await api.get<Page<AuditEntry>>(`/api/v1/audit?limit=30&cursor=${cursor}${action ? `&action=${encodeURIComponent(action)}` : ''}`);
    setExtra((x) => [...x, ...r.items]); setCursor(r.next_cursor);
  };
  const lastParams = rules.data?.length ? JSON.stringify(rules.data[rules.data.length - 1].params, null, 2) : '';

  return (
    <>
      <PageHead title="Yönetim" />
      <div className="grid g2">
        <Card title="Kullanıcılar">
          <AsyncView state={users}>
            {(rows) => (
              <Table head={['E-posta', 'Rol', 'Ad']}>
                {rows.map((u) => <tr key={u.id}><td className="mono"><small>{u.email}</small></td><td>{ROLE_TR[u.role]}</td><td>{u.full_name}</td></tr>)}
              </Table>
            )}
          </AsyncView>
          <h3>Yeni kullanıcı</h3>
          <Form submit="Ekle" onSubmit={async (v) => {
            const u = await api.post<User>('/api/v1/users', { ...v, hospital_id: v.hospital_id ? Number(v.hospital_id) : null });
            users.reload(); return `Eklendi: ${u.email}`;
          }}>
            <div className="form-grid">
              <Field label="E-posta"><input name="email" type="email" required /></Field>
              <Field label="Ad soyad"><input name="full_name" required /></Field>
              <Field label="Parola"><input name="password" type="password" minLength={8} required /></Field>
              <Field label="Rol"><select name="role">{(Object.keys(ROLE_TR) as Role[]).filter((r) => r !== 'donor').map((r) => <option key={r} value={r}>{ROLE_TR[r]}</option>)}</select></Field>
              <Field label="Hastane id (rol hastane ise)"><input name="hospital_id" type="number" /></Field>
            </div>
          </Form>
        </Card>
        <Card title="Uygunluk kural sürümleri">
          <p className="sub">Yeni sürüm yalnızca ileri bir tarihten geçerli olur; o güne kadar mevcut sürüm uygulanır.</p>
          <AsyncView state={rules}>
            {(rows) => (
              <Table head={['Başlangıç', 'Bitiş', 'Not']}>
                {rows.map((r) => <tr key={r.id}><td>{fmtDate(r.valid_from)}</td><td>{r.valid_to ? fmtDate(r.valid_to) : 'açık'}</td><td>{r.note}</td></tr>)}
              </Table>
            )}
          </AsyncView>
          <Form className="mt-s" submit="Sürüm ekle" onSubmit={async (v) => {
            let p: unknown;
            try { p = JSON.parse(v.params); } catch { throw new Error('Parametreler geçerli JSON değil'); }
            await api.post('/api/v1/rules', { code: 'eligibility', valid_from: v.valid_from, note: v.note, params: p });
            rules.reload(); return 'Sürüm eklendi';
          }}>
            <Field label="Geçerlilik başlangıcı"><input type="date" name="valid_from" required /></Field>
            <Field label="Parametreler (JSON)"><textarea ref={params} name="params" rows={8} key={lastParams} defaultValue={lastParams} /></Field>
            <Field label="Not"><input name="note" /></Field>
          </Form>
        </Card>
      </div>
      <Card title="Denetim kaydı (eklemeli-yalnız)" className="mt">
        <Form className="inline" submit="Filtrele" primary={false} onSubmit={async (v) => setAction(v.action.trim())}>
          <Field label="İşlem"><input name="action" placeholder="ör. donor.health.read" /></Field>
        </Form>
        <div className="mt-s">
          <AsyncView state={audit}>
            {(r) => (
              <>
                <Table head={['Zaman', 'İşlem', 'Varlık', 'Aktör', 'Gerekçe']}>
                  {[...r.items, ...extra].map((e) => (
                    <tr key={e.id}><td>{fmtDT(e.at)}</td><td className="mono"><small>{e.action}</small></td><td>{e.entity} {e.entity_id}</td>
                      <td>{e.actor_role ? ROLE_TR[e.actor_role] ?? e.actor_role : 'sistem'} {e.actor_id}</td><td>{e.reason}</td></tr>
                  ))}
                </Table>
                {cursor && <button className="mt-s" onClick={more}>Daha fazla</button>}
              </>
            )}
          </AsyncView>
        </div>
      </Card>
    </>
  );
}
