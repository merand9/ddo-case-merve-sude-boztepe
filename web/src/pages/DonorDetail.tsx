import { useRef, useState, type ReactNode, type RefObject } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, type Appointment, type Decision, type Deferral, type Donation, type Donor, type Page } from '../api';
import { AsyncView, Badge, Card, DecisionBox, ErrorBox, Field, Form, Notice, Table } from '../components/ui';
import { DEFERRAL_OPTIONS, DONATION_TR, fmtDate, fmtDT, isoDay } from '../format';
import { useApi } from '../hooks';

interface HealthInfo { medications: string[]; conditions: string[]; notes?: string }

export default function DonorDetail() {
  const { id } = useParams();
  const donor = useApi(() => api.get<Donor>(`/api/v1/donors/${id}`), [id]);
  const deferrals = useApi(() => api.get<Deferral[]>(`/api/v1/donors/${id}/deferrals`), [id]);
  const donations = useApi(() => api.get<Page<Donation>>(`/api/v1/donations?donor_id=${id}&limit=20`), [id]);
  const eligForm = useRef<HTMLFormElement>(null);
  const [recBusy, setRecBusy] = useState(false);
  const [recOut, setRecOut] = useState<ReactNode>();

  const eligBody = (v: Record<string, string>) => ({
    donation_type: v.donation_type,
    measurements: { hemoglobin: Number(v.hemoglobin), weight_kg: Number(v.weight_kg) },
    declarations: v.decl ? [{ code: v.decl, date: v.decl_date }] : [],
  });

  const record = async () => {
    if (!eligForm.current) return;
    const v = Object.fromEntries(new FormData(eligForm.current)) as Record<string, string>;
    const b = eligBody(v);
    setRecBusy(true); setRecOut(undefined);
    try {
      const d = await api.post<Donation>('/api/v1/donations', {
        donor_id: Number(id), donation_type: v.donation_type, volume_ml: v.donation_type === 'whole_blood' ? 450 : 250,
        hemoglobin: b.measurements.hemoglobin, weight_kg: b.measurements.weight_kg, declarations: b.declarations,
      });
      setRecOut(<Notice kind="ok">Bağış kaydedildi: <span className="mono">{d.bag_no}</span>. Bileşen üretimi ve test laboratuvar ekranından yapılır.</Notice>);
      donations.reload();
    } catch (e) { setRecOut(<ErrorBox error={e as Error} />); } finally { setRecBusy(false); }
  };

  return (
    <>
      <p><Link to="/donors">← Bağışçılar</Link></p>
      <AsyncView state={donor}>
        {(d) => (
          <div className="card">
            <h1>{d.first_name} {d.last_name} <span className="grp">{d.group}</span></h1>
            <p className="sub"><span className="mono">{d.donor_no}</span> · {d.sex === 'F' ? 'Kadın' : 'Erkek'} · Doğum {fmtDate(d.birth_date)} · {d.phone || 'telefon yok'}</p>
          </div>
        )}
      </AsyncView>

      <div className="grid g2 mt">
        <Card title="Uygunluk kontrolü">
          <form ref={eligForm} onSubmit={(e) => e.preventDefault()} id="elig">
            <div className="form-grid">
              <Field label="Bağış türü"><select name="donation_type"><option value="whole_blood">Tam kan</option><option value="platelet_apheresis">Trombosit aferezi</option></select></Field>
              <Field label="Hemoglobin (g/dL)"><input name="hemoglobin" type="number" step="0.1" defaultValue="14.0" /></Field>
              <Field label="Kilo (kg)"><input name="weight_kg" type="number" step="0.1" defaultValue="70" /></Field>
              <Field label="Beyan (isteğe bağlı)">
                <select name="decl"><option value="">Yok</option>{DEFERRAL_OPTIONS.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select>
              </Field>
              <Field label="Beyan tarihi"><input name="decl_date" type="date" defaultValue={isoDay()} /></Field>
            </div>
          </form>
          <EligibilityCheck formRef={eligForm} id={id!} body={eligBody}>
            <button onClick={record} disabled={recBusy}>{recBusy ? 'Kaydediliyor…' : 'Bağışı kaydet'}</button>
          </EligibilityCheck>
          {recOut}
        </Card>

        <Card title="Randevu">
          <Form className="inline" submit="Randevu ver" onSubmit={async (v) => {
            const a = await api.post<Appointment>('/api/v1/appointments', { donor_id: Number(id), scheduled_at: new Date(v.at).toISOString(), donation_type: v.type });
            return `Randevu oluşturuldu: ${fmtDT(a.scheduled_at)}`;
          }}>
            <Field label="Tarih-saat"><input name="at" type="datetime-local" required /></Field>
            <Field label="Tür"><select name="type"><option value="whole_blood">Tam kan</option><option value="platelet_apheresis">Trombosit</option></select></Field>
          </Form>
          <h2>Erteleme ekle</h2>
          <Form className="inline" submit="Ekle" primary={false} onSubmit={async (v) => {
            await api.post(`/api/v1/donors/${id}/deferrals`, v); deferrals.reload(); return 'Erteleme eklendi';
          }}>
            <Field label="Neden"><select name="reason_code">{DEFERRAL_OPTIONS.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select></Field>
            <Field label="Başlangıç"><input name="starts_on" type="date" defaultValue={isoDay()} /></Field>
          </Form>
          <div className="mt-s">
            <AsyncView state={deferrals} empty="Erteleme kaydı yok">
              {(rows) => (
                <Table head={['Neden', 'Başlangıç', 'Bitiş']}>
                  {rows.map((r) => <tr key={r.id}><td className="mono">{r.reason_code}</td><td>{fmtDate(r.starts_on)}</td>
                    <td>{r.permanent ? <span className="badge b-bad">Kalıcı</span> : fmtDate(r.ends_on)}</td></tr>)}
                </Table>
              )}
            </AsyncView>
          </div>
        </Card>
      </div>

      <Card title="Sağlık beyanı (şifreli)" className="mt">
        <Form className="inline" submit="Görüntüle" primary={false} onSubmit={async (v) => {
          const h = await api.get<HealthInfo>(`/api/v1/donors/${id}/health`, { 'X-Access-Reason': encodeURIComponent(v.reason) });
          return (
            <Notice kind="info">
              <b>İlaçlar:</b> {h.medications.join(', ') || 'yok'}<br /><b>Hastalıklar:</b> {h.conditions.join(', ') || 'yok'}
              {h.notes && <><br /><b>Not:</b> {h.notes}</>}<br /><small>Bu erişim gerekçesiyle denetim kaydına yazıldı.</small>
            </Notice>
          );
        }}>
          <Field label="Erişim gerekçesi (zorunlu, denetime yazılır)" grow>
            <input name="reason" minLength={10} required placeholder="ör. Bağış öncesi muayene ilaç kontrolü" />
          </Field>
        </Form>
      </Card>

      <Card title="Bağış geçmişi" className="mt">
        <AsyncView state={donations} empty="Henüz bağış yok">
          {(r) => (
            <Table head={['Torba', 'Tür', 'Tarih', 'Hb', 'Test', 'Ürün']}>
              {r.items.map((d) => (
                <tr key={d.id}><td className="mono">{d.bag_no}</td><td>{DONATION_TR[d.donation_type]}</td><td>{fmtDT(d.collected_at)}</td>
                  <td className="num">{d.hemoglobin}</td><td>{d.test_outcome ? <Badge s={d.test_outcome} /> : <Badge s="pending" />}</td><td className="num">{d.products}</td></tr>
              ))}
            </Table>
          )}
        </AsyncView>
      </Card>
    </>
  );
}

function EligibilityCheck({ formRef, id, body, children }: {
  formRef: RefObject<HTMLFormElement | null>; id: string; body: (v: Record<string, string>) => unknown; children?: ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [out, setOut] = useState<ReactNode>();
  return (
    <>
      <div className="actions mt-s">
        <button className="primary" disabled={busy} onClick={async () => {
          if (!formRef.current) return;
          setBusy(true); setOut(undefined);
          try {
            const v = Object.fromEntries(new FormData(formRef.current)) as Record<string, string>;
            setOut(<DecisionBox d={await api.post<Decision>(`/api/v1/donors/${id}/eligibility`, body(v))} />);
          } catch (e) { setOut(<ErrorBox error={e as Error} />); } finally { setBusy(false); }
        }}>Kontrol et</button>
        {children}
      </div>
      {out}
    </>
  );
}
