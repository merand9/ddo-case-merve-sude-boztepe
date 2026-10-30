import { api, type StockRow } from '../api';
import { AsyncView, Badge, Card, Empty, PageHead, StockMatrix } from '../components/ui';
import { COMP_TR, fmtTime } from '../format';
import { useApi } from '../hooks';

interface LiveData {
  last_5_minutes: { donations: number; ineligible_attempts: number; requests: number; issued: number };
  product_events: { at: string; product_no: string; component: string; group: string; from?: string; to: string }[];
  actions: { at: string; action: string; entity: string; entity_id?: string }[];
}

export default function Live() {
  const live = useApi(() => api.get<LiveData>('/api/v1/live'), [], 5000);
  const stock = useApi(() => api.get<StockRow[]>('/api/v1/stock'), [], 5000);
  return (
    <>
      <PageHead title="Canlı pano" sub="5 saniyede bir yenilenir. Simülatör sürekli olay üretir." />
      <AsyncView state={live}>
        {(d) => (
          <div className="grid g3">
            {([['Bağış', d.last_5_minutes.donations], ['Uygunsuz deneme', d.last_5_minutes.ineligible_attempts],
              ['Hastane talebi', d.last_5_minutes.requests], ['Dağıtılan ünite', d.last_5_minutes.issued]] as const).map(([k, v]) => (
              <div key={k} className="card stat"><b>{v}</b><span>{k} · son 5 dk</span></div>
            ))}
          </div>
        )}
      </AsyncView>
      <Card title="Stok matrisi (kullanılabilir ünite)" className="mt">
        <AsyncView state={stock}>{(rows) => <StockMatrix rows={rows} />}</AsyncView>
        <p className="sub mt-s"><span className="badge b-warn">Düşük</span> <span className="badge b-bad">Kritik</span> stok alarmı, tahmini tüketime göre kaç gün yeteceğine bakılarak verilir.</p>
      </Card>
      <div className="grid g2 mt">
        <Card title="Son ürün olayları">
          <AsyncView state={live} isEmpty={(d) => d.product_events.length === 0} empty="Henüz olay yok">
            {(d) => (
              <ul className="feed">
                {d.product_events.map((e, i) => (
                  <li key={i}><time>{fmtTime(e.at)}</time><span className="mono">{e.product_no}</span><span className="grp">{e.group}</span>
                    {COMP_TR[e.component]} {e.from && <><Badge s={e.from} /> →</>} <Badge s={e.to} /></li>
                ))}
              </ul>
            )}
          </AsyncView>
        </Card>
        <Card title="Son kritik işlemler">
          {live.data && live.data.actions.length === 0 ? <Empty>Henüz işlem yok</Empty> : (
            <AsyncView state={live}>
              {(d) => (
                <ul className="feed">
                  {d.actions.map((a, i) => <li key={i}><time>{fmtTime(a.at)}</time><span className="mono">{a.action}</span><span>{a.entity} {a.entity_id}</span></li>)}
                </ul>
              )}
            </AsyncView>
          )}
        </Card>
      </div>
    </>
  );
}
