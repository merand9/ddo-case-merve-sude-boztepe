import { useState } from 'react';
import { api, type Page, type Product, type StockRow } from '../api';
import { useAuth } from '../auth';
import { ActButton, AsyncView, Badge, Card, ComponentSelect, Field, Form, GroupSelect, Notice, PageHead, StockMatrix, Table } from '../components/ui';
import { COMP_TR, fmtDT, grp } from '../format';
import { useApi } from '../hooks';

interface Verify { valid: boolean; product_no?: string; component?: string; group?: string; status?: string; expires_at?: string; reason?: string }

export default function Stock() {
  const { user } = useAuth();
  const isBank = user?.role === 'bank';
  const [f, setF] = useState<Record<string, string>>({ status: 'available', component: '', group: '' });
  const stock = useApi(() => api.get<StockRow[]>('/api/v1/stock'));
  const products = useApi(() => {
    const q = new URLSearchParams({ ...Object.fromEntries(Object.entries(f).filter(([, v]) => v)), sort: 'expiry', limit: '100' });
    return api.get<Page<Product>>(`/api/v1/products?${q}`);
  }, [f]);
  const refresh = () => { products.reload(); stock.reload(); };

  return (
    <>
      <PageHead title="Stok" sub="Ürün listesi SKT'ye göre sıralıdır (FEFO: ilk dolan ilk çıkar)." />
      <Card><AsyncView state={stock}>{(rows) => <StockMatrix rows={rows} />}</AsyncView></Card>
      <Form className="inline card mt" submit="Listele" onSubmit={async (v) => setF(v)}>
        <Field label="Durum">
          <select name="status" defaultValue="available">
            <option value="available">Stokta</option><option value="reserved">Rezerve</option><option value="pending_test">Test bekliyor</option>
            <option value="quarantined">Karantina</option><option value="issued">Dağıtıldı</option><option value="expired">SKT doldu</option><option value="">Tümü</option>
          </select>
        </Field>
        <Field label="Bileşen"><ComponentSelect all /></Field>
        <Field label="Grup"><GroupSelect all="Tümü" /></Field>
      </Form>
      <div className="mt">
        <AsyncView state={products} empty="Bu filtreyle ürün yok">
          {(r) => (
            <Table head={['Ürün', 'Bileşen', 'Grup', 'ml', 'SKT', 'Durum', 'Barkod', '']}>
              {r.items.map((p) => (
                <tr key={p.id}>
                  <td className="mono">{p.product_no}{p.parent_id && <small> (alt)</small>}</td><td>{COMP_TR[p.component]}</td>
                  <td className="grp">{grp(p.abo, p.rh_positive)}</td><td className="num">{p.volume_ml}</td><td>{fmtDT(p.expires_at)}</td>
                  <td><Badge s={p.status} /></td><td className="mono"><small>{p.barcode}</small></td>
                  <td className="actions">
                    {isBank && p.status === 'available' && p.component !== 'FFP' && (
                      <ActButton onClick={async () => {
                        const k = await api.post<Product[]>(`/api/v1/products/${p.id}/split`, { parts: 2 });
                        refresh(); return `${k.length} alt ürün oluşturuldu`;
                      }}>Böl</ActButton>
                    )}
                    {['available', 'quarantined', 'pending_test'].includes(p.status) && (
                      <ActButton onClick={async () => {
                        const reason = window.prompt('İmha gerekçesi:');
                        if (!reason) return;
                        await api.post(`/api/v1/products/${p.id}/discard`, { reason });
                        refresh(); return 'İmha edildi';
                      }}>İmha</ActButton>
                    )}
                  </td>
                </tr>
              ))}
            </Table>
          )}
        </AsyncView>
      </div>
      <Card title="İmzalı barkod doğrulama" className="mt">
        <Form className="inline" submit="Doğrula" primary={false} onSubmit={async (v) => {
          const r = await api.get<Verify>(`/api/v1/products/verify?code=${encodeURIComponent(v.code.trim())}`);
          return r.valid
            ? <Notice kind="ok">İmza geçerli: <b>{r.product_no}</b> · {COMP_TR[r.component!]} <span className="grp">{r.group}</span> · <Badge s={r.status!} /> · SKT {fmtDT(r.expires_at)}</Notice>
            : <Notice kind="bad">İmza geçersiz{r.reason ? `: ${r.reason}` : ''}. Ürün kullanılmamalı.</Notice>;
        }}>
          <Field label="Barkod" grow><input name="code" placeholder="P00001234.ABCDEFGH12" required /></Field>
        </Form>
      </Card>
    </>
  );
}
