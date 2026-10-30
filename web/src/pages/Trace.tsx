import { api } from '../api';
import { Badge, Card, Field, Form, PageHead, Table } from '../components/ui';
import { COMP_TR, fmtDate } from '../format';

interface Node { product_id: number; product_no: string; depth: number; component: string; status: string; hospital?: string; patient_ref?: string }
interface Forward { donor: { donor_no: string }; products: Node[]; patients: string[] }
interface Back {
  links: { transfused_product_no: string; root_product_no: string; hops: number; donation_id: number; collected_at: string; donor_id: number; donor_no: string }[];
  other_recipients_of_same_donors: string[];
}

export default function Trace() {
  return (
    <>
      <PageHead title="Geri izlenebilirlik" sub="Bağışçıdan hastaya ve hastadan bağışçıya (look-back). Bölünmüş ürünler recursive sorguyla izlenir." />
      <div className="grid g2">
        <Card title="Bağışçı → hasta">
          <Form className="inline" submit="İzle" onSubmit={async (v) => {
            const r = await api.get<Forward>(`/api/v1/trace/donors/${v.id}`);
            return (
              <>
                <p><b>{r.donor.donor_no}</b> · {r.products.length} ürün · hastalar: {r.patients.join(', ') || '—'}</p>
                <Table head={['Ürün', 'Bileşen', 'Durum', 'Hastane', 'Hasta']}>
                  {r.products.map((p) => (
                    <tr key={p.product_id}><td className="mono" style={{ paddingLeft: `${0.65 + p.depth * 1.2}rem` }}>{p.depth ? '└ ' : ''}{p.product_no}</td>
                      <td>{COMP_TR[p.component]}</td><td><Badge s={p.status} /></td><td>{p.hospital}</td><td className="mono">{p.patient_ref}</td></tr>
                  ))}
                </Table>
              </>
            );
          }}>
            <Field label="Bağışçı id"><input name="id" type="number" required /></Field>
          </Form>
        </Card>
        <Card title="Hasta → bağışçı">
          <Form className="inline" submit="İzle" onSubmit={async (v) => {
            const r = await api.get<Back>(`/api/v1/trace/patients/${encodeURIComponent(v.ref)}`);
            return (
              <>
                <Table head={['Transfüze ürün', 'Kök ürün', 'Adım', 'Bağış', 'Bağışçı']}>
                  {r.links.map((l, i) => (
                    <tr key={i}><td className="mono">{l.transfused_product_no}</td><td className="mono">{l.root_product_no}</td><td className="num">{l.hops}</td>
                      <td>#{l.donation_id} · {fmtDate(l.collected_at)}</td><td className="mono">{l.donor_no} <small>(id {l.donor_id})</small></td></tr>
                  ))}
                </Table>
                <p className="sub">Aynı bağışçılardan ürün alan diğer hastalar: {r.other_recipients_of_same_donors.join(', ') || 'yok'}</p>
              </>
            );
          }}>
            <Field label="Hasta referansı"><input name="ref" required /></Field>
          </Form>
        </Card>
      </div>
    </>
  );
}
