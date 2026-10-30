import { api, type Donation, type Page, type Product, type TestChange, type TestOutcome } from '../api';
import { ActButton, AsyncView, Card, Field, Form, PageHead, Table, useToast } from '../components/ui';
import { DONATION_TR, fmtDT } from '../format';
import { useApi } from '../hooks';

const MARKERS = ['hiv', 'hbv', 'hcv', 'syphilis'] as const;

export default function Lab() {
  const toast = useToast();
  const comps = useApi(() => api.get<Page<Donation>>('/api/v1/donations?pending=components&limit=20'));
  const tests = useApi(() => api.get<Page<Donation>>('/api/v1/donations?pending=tests&limit=20'));
  const changes = useApi(() => api.get<TestChange[]>('/api/v1/test-changes?status=pending'));

  const enter = async (id: number, marker?: string) => {
    const body = Object.fromEntries(MARKERS.map((m) => [m, m === marker]));
    const r = await api.post<TestOutcome>(`/api/v1/donations/${id}/test-results`, body);
    tests.reload();
    if (r.outcome === 'positive') {
      toast(`Pozitif: ${r.quarantined.length} ürün karantinada, ${r.recalls.length} geri çağırma, look-back ${r.lookback_donations.length} bağış`, true);
      return;
    }
    return `Negatif: ${r.released} ürün stoğa alındı`;
  };

  return (
    <>
      <PageHead title="Laboratuvar" sub="Pozitif sonuç girildiğinde bağışın tüm türev ürünleri (bölünmüşler dahil) karantinaya alınır, bağışçı kalıcı reddedilir." />
      <div className="grid g2">
        <Card title="Bileşen üretimi bekleyen bağışlar">
          <AsyncView state={comps} empty="Üretim bekleyen bağış yok">
            {(r) => (
              <Table head={['Torba', 'Tür', 'Toplama', '']}>
                {r.items.map((d) => (
                  <tr key={d.id}><td className="mono">{d.bag_no}</td><td>{DONATION_TR[d.donation_type]}</td><td>{fmtDT(d.collected_at)}</td>
                    <td><ActButton primary onClick={async () => {
                      const ps = await api.post<Product[]>(`/api/v1/donations/${d.id}/components`);
                      comps.reload(); return `${ps.length} ürün üretildi`;
                    }}>Üret</ActButton></td></tr>
                ))}
              </Table>
            )}
          </AsyncView>
        </Card>
        <Card title="Test sonucu bekleyen bağışlar">
          <AsyncView state={tests} empty="Test bekleyen bağış yok">
            {(r) => (
              <Table head={['Torba', 'Toplama', 'Ürün', 'Sonuç']}>
                {r.items.map((d) => (
                  <tr key={d.id}>
                    <td className="mono">{d.bag_no} <small>#{d.id}</small></td><td>{fmtDT(d.collected_at)}</td><td className="num">{d.products}</td>
                    <td className="actions">
                      <ActButton onClick={() => enter(d.id)}>Negatif</ActButton>
                      <select aria-label="Pozitif belirteç" value="" onChange={(e) => {
                        const m = e.target.value;
                        if (m && window.confirm(`Bağış #${d.id} için ${m.toUpperCase()} pozitif girilsin mi? Tüm türev ürünler karantinaya alınır.`)) {
                          enter(d.id, m).catch((err: Error) => toast(err.message, true));
                        }
                      }}>
                        <option value="">Pozitif…</option>{MARKERS.map((m) => <option key={m} value={m}>{m}</option>)}
                      </select>
                    </td>
                  </tr>
                ))}
              </Table>
            )}
          </AsyncView>
        </Card>
      </div>

      <Card title="Test sonucu düzeltme (çift onay)" className="mt">
        <p className="sub">Talebi açan uzman onaylayamaz; ikinci bir laboratuvar uzmanı onaylar.</p>
        <Form submit="Talep oluştur" onSubmit={async (v) => {
          const proposed = Object.fromEntries(MARKERS.map((m) => [m, v[m] === 'true']));
          const c = await api.post<TestChange>(`/api/v1/donations/${v.donation_id}/test-changes`, { proposed, reason: v.reason });
          changes.reload();
          return `Talep #${c.id} oluşturuldu; ikinci onay bekleniyor.`;
        }}>
          <div className="form-grid">
            <Field label="Bağış no (id)"><input name="donation_id" type="number" required /></Field>
            {MARKERS.map((m) => (
              <Field key={m} label={m.toUpperCase()}><select name={m}><option value="false">Negatif</option><option value="true">Pozitif</option></select></Field>
            ))}
            <div className="span-all"><Field label="Gerekçe"><input name="reason" required minLength={5} /></Field></div>
          </div>
        </Form>
        <h3>Bekleyen talepler</h3>
        <AsyncView state={changes} empty="Bekleyen talep yok">
          {(rows) => (
            <Table head={['Talep', 'Bağış', 'Önerilen', 'Gerekçe', 'Açan', '']}>
              {rows.map((c) => (
                <tr key={c.id}><td>#{c.id}</td><td>#{c.donation_id}</td>
                  <td className="mono">{Object.entries(c.proposed).filter(([, v]) => v).map(([k]) => k).join(', ') || 'tümü negatif'}</td>
                  <td>{c.reason}</td><td>kullanıcı {c.requested_by}</td>
                  <td className="actions">
                    <ActButton primary onClick={async () => {
                      const r = await api.post<{ effect: TestOutcome }>(`/api/v1/test-changes/${c.id}/approve`);
                      changes.reload(); return `Onaylandı: sonuç ${r.effect.outcome}`;
                    }}>Onayla</ActButton>
                    <ActButton onClick={async () => { await api.post(`/api/v1/test-changes/${c.id}/reject`); changes.reload(); return 'Reddedildi'; }}>Reddet</ActButton>
                  </td></tr>
              ))}
            </Table>
          )}
        </AsyncView>
      </Card>
    </>
  );
}
