import { useMemo, useState } from 'react';
import { Bar, BarChart, CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { api } from '../api';
import { AsyncView, Card, ComponentSelect, Empty, Field, Form, GroupSelect, PageHead, Table } from '../components/ui';
import { COMP_TR, addDays, fmtDate } from '../format';
import { useApi } from '../hooks';

interface GroupForecast { group: string; available: number; avg_daily_28d: number; forecast: number[]; days_of_supply: number; expected_expiry_loss: number; level: string }
interface ForecastReport { groups: GroupForecast[]; method: string }
interface BT { mape: number; wape: number; bias: number; days: number; points?: { day: string; actual: number; forecast: number }[] }
interface Backtest { total: BT; groups: Record<string, BT> }
interface Alert { id: number; component: string; group: string; level: string; available_units: number; days_of_supply: number }
interface Cand { donor_id: number; donor_no: string; name: string; phone?: string; last_donation?: string }

const LevelBadge = ({ l }: { l: string }) =>
  l === 'critical' ? <span className="badge b-bad">Kritik</span> : l === 'low' ? <span className="badge b-warn">Düşük</span> : null;

export default function Forecast() {
  const [comp, setComp] = useState('RBC');
  const [callGroup, setCallGroup] = useState<string | null>(null);
  const fc = useApi(() => api.get<ForecastReport>(`/api/v1/forecast?component=${comp}&horizon=7`), [comp]);
  const bt = useApi(() => api.get<Backtest>(`/api/v1/forecast/backtest?component=${comp}&days=28`), [comp]);
  const alerts = useApi(() => api.get<Alert[]>('/api/v1/alerts'));
  const calls = useApi(() => callGroup === null ? Promise.resolve(null)
    : api.get<Record<string, Cand[]>>(`/api/v1/call-list?limit=30${callGroup ? `&group=${encodeURIComponent(callGroup)}` : ''}`), [callGroup]);

  const days = useMemo(() => Array.from({ length: 7 }, (_, i) => addDays(i).toLocaleDateString('tr-TR', { weekday: 'short' })), []);
  const backtestSeries = useMemo(() => (bt.data?.total.points ?? []).map((p) => ({
    gün: new Date(p.day).toLocaleDateString('tr-TR', { day: '2-digit', month: '2-digit' }), Gerçek: p.actual, Tahmin: p.forecast,
  })), [bt.data]);

  return (
    <>
      <PageHead title="Stok tahmini ve çağrı listesi" sub="28 günlük hareketli ortalama × haftanın günü çarpanı. SKT kaybı FEFO varsayımıyla simüle edilir.">
        <Form className="inline" submit="Hesapla" primary={false} onSubmit={async (v) => setComp(v.component)}>
          <Field label="Bileşen"><ComponentSelect /></Field>
        </Form>
      </PageHead>

      <div className="grid g2">
        <Card title="Açık alarmlar">
          <AsyncView state={alerts} empty="Açık alarm yok">
            {(rows) => (
              <Table head={['Bileşen', 'Grup', 'Seviye', 'Stok', 'Gün']}>
                {rows.map((a) => (
                  <tr key={a.id}><td>{COMP_TR[a.component]}</td><td className="grp">{a.group}</td><td><LevelBadge l={a.level} /></td>
                    <td className="num">{a.available_units}</td><td className="num">{a.days_of_supply}</td>
                  </tr>
                ))}
              </Table>
            )}
          </AsyncView>
        </Card>
        <Card title="Geriye dönük test (son 28 gün)">
          <AsyncView state={bt}>
            {(r) => (
              <>
                <div className="grid g3">
                  <div className="stat"><b>{r.total.mape}%</b><span>MAPE (toplam)</span></div>
                  <div className="stat"><b>{r.total.wape}%</b><span>WAPE (toplam)</span></div>
                  <div className="stat"><b>{r.total.bias}</b><span>Ortalama sapma (ünite/gün)</span></div>
                </div>
                <div className="chart">
                  <ResponsiveContainer width="100%" height={200}>
                    <LineChart data={backtestSeries} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
                      <CartesianGrid strokeDasharray="3 3" stroke="#e4e1dc" />
                      <XAxis dataKey="gün" tick={{ fontSize: 11 }} interval={3} />
                      <YAxis tick={{ fontSize: 11 }} />
                      <Tooltip />
                      <Legend />
                      <Line type="monotone" dataKey="Gerçek" stroke="#1d1c1a" strokeWidth={2} dot={false} />
                      <Line type="monotone" dataKey="Tahmin" stroke="#b4232c" strokeWidth={2} strokeDasharray="5 3" dot={false} />
                    </LineChart>
                  </ResponsiveContainer>
                </div>
                <details>
                  <summary>Grup bazında hata</summary>
                  <Table head={['Grup', 'MAPE', 'WAPE']}>
                    {Object.entries(r.groups).map(([g, x]) => <tr key={g}><td className="grp">{g}</td><td className="num">{x.mape}%</td><td className="num">{x.wape}%</td></tr>)}
                  </Table>
                  <p className="sub">Düşük hacimli gruplarda MAPE yüksek çıkar (birkaç ünitelik hata büyük yüzdedir); bu yüzden WAPE de raporlanır.</p>
                </details>
              </>
            )}
          </AsyncView>
        </Card>
      </div>

      <Card title={`7 günlük tahmin — ${COMP_TR[comp]}`} className="mt">
        <AsyncView state={fc}>
          {(r) => (
            <>
              <div className="chart">
                <ResponsiveContainer width="100%" height={220}>
                  <BarChart data={r.groups.map((g) => ({ grup: g.group, Stok: g.available, '7 gün talep': Math.round(g.forecast.reduce((a, b) => a + b, 0)) }))}
                    margin={{ top: 8, right: 8, left: -10, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#e4e1dc" />
                    <XAxis dataKey="grup" tick={{ fontSize: 12 }} />
                    <YAxis tick={{ fontSize: 11 }} />
                    <Tooltip />
                    <Legend />
                    <Bar dataKey="Stok" fill="#2f7d4f" radius={[4, 4, 0, 0]} />
                    <Bar dataKey="7 gün talep" fill="#b4232c" radius={[4, 4, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
              <Table head={['Grup', 'Stok', 'Ort./gün', ...days, 'Yeterlilik (gün)', 'SKT kaybı', '']}>
                {r.groups.map((g) => (
                  <tr key={g.group}>
                    <td className="grp">{g.group}</td><td className="num">{g.available}</td><td className="num">{g.avg_daily_28d}</td>
                    {g.forecast.map((v, i) => <td key={i} className="num">{v.toFixed(1)}</td>)}
                    <td className="num"><b>{g.days_of_supply >= 999 ? '∞' : g.days_of_supply}</b></td>
                    <td className="num">{g.expected_expiry_loss}</td><td><LevelBadge l={g.level} /></td>
                  </tr>
                ))}
              </Table>
            </>
          )}
        </AsyncView>
      </Card>

      <Card title="Çağrı listesi" className="mt">
        <Form className="inline" submit="Listele" primary={false} onSubmit={async (v) => setCallGroup(v.group)}>
          <Field label="Grup"><GroupSelect all="Alarmdaki gruplar" /></Field>
        </Form>
        <div className="mt-s">
          {callGroup === null ? <p className="sub">Kritik gruptaki, bugün bağış yapabilecek bağışçıları listelemek için bir grup seçin.</p> : (
            <AsyncView state={calls} isEmpty={(m) => !m || Object.keys(m).length === 0} empty="Alarmda grup yok; bir grup seçin.">
              {(m) => Object.entries(m!).map(([g, list]) => (
                <div key={g}>
                  <h3><span className="grp">{g}</span> · {list.length} uygun bağışçı</h3>
                  {list.length === 0 ? <Empty>Uygun bağışçı yok</Empty> : (
                    <Table head={['No', 'Ad', 'Telefon', 'Son bağış']}>
                      {list.map((c) => <tr key={c.donor_id}><td className="mono">{c.donor_no}</td><td>{c.name}</td><td>{c.phone}</td><td>{fmtDate(c.last_donation)}</td></tr>)}
                    </Table>
                  )}
                </div>
              ))}
            </AsyncView>
          )}
        </div>
      </Card>
    </>
  );
}
