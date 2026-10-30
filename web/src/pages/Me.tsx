import { api, type Appointment, type Decision, type Donor } from '../api';
import { ActButton, AsyncView, Card, DecisionBox, Empty, Field, Form, Notice, PageHead, Table } from '../components/ui';
import { DONATION_TR, fmtDate, fmtDT } from '../format';
import { useApi } from '../hooks';

interface MyView {
  donor: Donor; eligibility: Decision; appointments: Appointment[];
  donations: { id: number; donation_type: string; collected_at: string; volume_ml: number }[];
}

export default function Me() {
  const me = useApi(() => api.get<MyView>('/api/v1/me/donor'));
  return (
    <AsyncView state={me}>
      {(v) => {
        const upcoming = v.appointments.find((a) => a.status === 'booked');
        const days = upcoming ? Math.ceil((new Date(upcoming.scheduled_at).getTime() - Date.now()) / 86400000) : null;
        return (
          <>
            <PageHead title={`Merhaba ${v.donor.first_name}`} sub={<>Kan grubunuz <span className="grp">{v.donor.group}</span> · Bağışçı no <span className="mono">{v.donor.donor_no}</span></>} />
            {upcoming && (
              <Notice kind="info"><b>Hatırlatma:</b> {fmtDT(upcoming.scheduled_at)} tarihinde randevunuz var
                {days != null && ` (${days <= 0 ? 'bugün' : `${days} gün sonra`})`}. Bağıştan önce iyi beslenin ve bol su için.</Notice>
            )}
            <div className="grid g2">
              <Card title="Bağışa uygunluk"><DecisionBox d={v.eligibility} /></Card>
              <Card title="Randevu">
                {upcoming ? (
                  <>
                    <p>Açık randevunuz: <b>{fmtDT(upcoming.scheduled_at)}</b></p>
                    <ActButton onClick={async () => { await api.post(`/api/v1/me/appointments/${upcoming.id}/cancel`); me.reload(); return 'Randevu iptal edildi'; }}>Randevuyu iptal et</ActButton>
                  </>
                ) : (
                  <>
                    <Form className="inline" submit="Randevu al" onSubmit={async (f) => {
                      await api.post('/api/v1/me/appointments', { scheduled_at: new Date(f.at).toISOString() }); me.reload();
                    }}>
                      <Field label="Tarih-saat"><input type="datetime-local" name="at" required min={new Date().toISOString().slice(0, 16)} /></Field>
                    </Form>
                    {v.eligibility.next_eligible_at && <p className="sub">En erken uygun gün: <b>{fmtDate(v.eligibility.next_eligible_at)}</b></p>}
                  </>
                )}
              </Card>
            </div>
            <Card title="Bağış geçmişim" className="mt">
              {v.donations.length ? (
                <Table head={['Tarih', 'Tür', 'ml']}>
                  {v.donations.map((d) => <tr key={d.id}><td>{fmtDT(d.collected_at)}</td><td>{DONATION_TR[d.donation_type]}</td><td className="num">{d.volume_ml}</td></tr>)}
                </Table>
              ) : <Empty>Henüz bağışınız yok — ilk bağışınız için randevu alabilirsiniz.</Empty>}
            </Card>
          </>
        );
      }}
    </AsyncView>
  );
}
