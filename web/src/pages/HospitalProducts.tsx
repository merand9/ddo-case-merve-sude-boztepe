import { useRef } from 'react';
import { api } from '../api';
import { AsyncView, Badge, Card, Field, Form, PageHead, Table } from '../components/ui';
import { COMP_TR, fmtDT } from '../format';
import { useApi } from '../hooks';

interface HProduct { id: number; product_no: string; component: string; group: string; expires_at: string; status: string; request_id: number; patient_ref?: string }

export default function HospitalProducts() {
  const list = useApi(() => api.get<HProduct[]>('/api/v1/hospital/products'));
  const no = useRef<HTMLInputElement>(null);
  const pat = useRef<HTMLInputElement>(null);
  return (
    <>
      <PageHead title="Hastanemize dağıtılan ürünler" sub="Bağışçı kimliği gösterilmez; yalnızca ürün bilgisi." />
      <Card title="Transfüzyon kaydı">
        <Form className="inline" submit="Kaydet" onSubmit={async (v) => { await api.post('/api/v1/transfusions', v); list.reload(); return 'Transfüzyon kaydedildi'; }}>
          <Field label="Ürün no"><input ref={no} name="product_no" required placeholder="P00001234" /></Field>
          <Field label="Hasta referansı"><input ref={pat} name="patient_ref" required minLength={3} /></Field>
        </Form>
      </Card>
      <div className="mt">
        <AsyncView state={list} empty="Henüz dağıtılan ürün yok">
          {(rows) => (
            <Table head={['Ürün', 'Bileşen', 'Grup', 'SKT', 'Durum', 'Talep', 'Hasta', '']}>
              {rows.map((p) => (
                <tr key={p.id}><td className="mono">{p.product_no}</td><td>{COMP_TR[p.component]}</td><td className="grp">{p.group}</td>
                  <td>{fmtDT(p.expires_at)}</td><td><Badge s={p.status} /></td><td>#{p.request_id}</td><td className="mono"><small>{p.patient_ref}</small></td>
                  <td>{p.status === 'issued' && <button className="small" onClick={() => {
                    if (no.current && pat.current) { no.current.value = p.product_no; pat.current.focus(); window.scrollTo({ top: 0, behavior: 'smooth' }); }
                  }}>Transfüzyon</button>}</td></tr>
              ))}
            </Table>
          )}
        </AsyncView>
      </div>
    </>
  );
}
