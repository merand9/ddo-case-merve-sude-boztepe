import { useState } from 'react';
import { api, type BloodRequest, type Page } from '../api';
import { useAuth } from '../auth';
import { ActButton, AsyncView, Badge, Card, ComponentSelect, Field, Form, GroupSelect, PageHead, Table, Urgency } from '../components/ui';
import { COMP_TR, STATUS_TR, fmtTime } from '../format';
import { useApi } from '../hooks';

export default function Requests() {
  const { user } = useAuth();
  const isHosp = user?.role === 'hospital';
  const isBank = user?.role === 'bank';
  const [status, setStatus] = useState('open,partial,reserved');
  const list = useApi(() => api.get<Page<BloodRequest>>(`/api/v1/requests?limit=50${status ? `&status=${status}` : ''}`), [status], 10000);

  return (
    <>
      <PageHead title={isHosp ? 'Kan talepleri' : 'Hastane talepleri'}
        sub="Tahsis: önce tam uyum, sonra uyumlu alternatifler; her kademede en erken SKT (FEFO). Çok acil talepler rutin rezervasyonları devralabilir.">
        <Form className="inline" submit="Filtrele" primary={false} onSubmit={async (v) => setStatus(v.status)}>
          <Field label="Durum">
            <select name="status" defaultValue={status}>
              <option value="open,partial,reserved">Bekleyen</option><option value="issued">Dağıtılan</option><option value="cancelled">İptal</option><option value="">Tümü</option>
            </select>
          </Field>
        </Form>
      </PageHead>

      {isHosp && (
        <Card title="Yeni talep">
          <Form submit="Talep gönder" onSubmit={async (v) => {
            const q = await api.post<BloodRequest>('/api/v1/requests', { ...v, units: Number(v.units) });
            list.reload();
            const got = q.reservations.filter((r) => r.status === 'active').length;
            return `Talep #${q.id}: ${got}/${q.units} ünite rezerve edildi (${STATUS_TR[q.status]}).`;
          }}>
            <div className="form-grid">
              <Field label="Bileşen"><ComponentSelect /></Field>
              <Field label="Hasta kan grubu"><GroupSelect defaultValue="A+" /></Field>
              <Field label="Ünite"><input name="units" type="number" min={1} max={20} defaultValue={1} /></Field>
              <Field label="Aciliyet"><select name="urgency"><option value="routine">Rutin</option><option value="urgent">Acil</option><option value="emergency">Çok acil</option></select></Field>
              <Field label="Hasta referansı (takma ad)"><input name="patient_ref" required minLength={3} placeholder="ör. PRT-2026-0042" /></Field>
            </div>
          </Form>
        </Card>
      )}

      <div className="mt">
        <AsyncView state={list} empty="Bu filtreyle talep yok">
          {(r) => (
            <Table head={['#', ...(isHosp ? [] : ['Hastane']), 'Bileşen', 'Grup', 'Ünite', 'Aciliyet', 'Hasta', 'Durum', 'Rezervasyon / ürün', '']}>
              {r.items.map((q) => (
                <tr key={q.id}>
                  <td>{q.id}</td>{!isHosp && <td>{q.hospital_name}</td>}<td>{COMP_TR[q.component]}</td><td className="grp">{q.group}</td>
                  <td className="num">{q.units}</td><td><Urgency u={q.urgency} /></td><td className="mono"><small>{q.patient_ref}</small></td>
                  <td><Badge s={q.status} /></td>
                  <td>
                    {q.reservations.filter((x) => x.status === 'active' || x.status === 'issued').map((x) => (
                      <div key={x.id}><span className="mono">{x.product_no}</span> <span className="grp">{x.group}</span>
                        {!x.exact_match && <> <span className="badge b-info">alternatif</span></>} <Badge s={x.status} />
                        {x.status === 'active' && <small> → {fmtTime(x.hold_until)}</small>}</div>
                    ))}
                    {q.reservations.every((x) => x.status !== 'active' && x.status !== 'issued') && '—'}
                  </td>
                  <td className="actions">
                    {isBank && ['open', 'partial'].includes(q.status) && <ActButton onClick={async () => {
                      const a = await api.post<{ reserved_product_ids: number[]; missing_units: number }>(`/api/v1/requests/${q.id}/allocate`);
                      list.reload();
                      return `${a.reserved_product_ids.length} ürün rezerve edildi${a.missing_units ? `, ${a.missing_units} eksik` : ''}`;
                    }}>Tahsis</ActButton>}
                    {isBank && ['reserved', 'partial'].includes(q.status) && <ActButton primary onClick={async () => {
                      await api.post(`/api/v1/requests/${q.id}/issue`); list.reload(); return 'Dağıtıldı';
                    }}>Dağıt</ActButton>}
                    {['open', 'partial', 'reserved'].includes(q.status) && <ActButton confirm={`Talep #${q.id} iptal edilsin mi?`} onClick={async () => {
                      await api.post(`/api/v1/requests/${q.id}/cancel`); list.reload(); return 'İptal edildi';
                    }}>İptal</ActButton>}
                  </td>
                </tr>
              ))}
            </Table>
          )}
        </AsyncView>
      </div>
    </>
  );
}
