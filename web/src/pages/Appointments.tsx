import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type Appointment } from '../api';
import { ActButton, AsyncView, Badge, Field, Form, PageHead, Table } from '../components/ui';
import { DONATION_TR, fmtDT, isoDay } from '../format';
import { useApi } from '../hooks';

export default function Appointments() {
  const [date, setDate] = useState(isoDay());
  const list = useApi(() => api.get<Appointment[]>(`/api/v1/appointments?date=${date}`), [date]);
  return (
    <>
      <PageHead title="Randevular">
        <Form className="inline" submit="Göster" primary={false} onSubmit={async (v) => setDate(v.date)}>
          <Field label="Gün"><input type="date" name="date" defaultValue={date} /></Field>
        </Form>
      </PageHead>
      <AsyncView state={list} empty="Bu gün için randevu yok">
        {(rows) => (
          <Table head={['Saat', 'Bağışçı', 'Tür', 'Durum', '']}>
            {rows.map((a) => (
              <tr key={a.id}><td>{fmtDT(a.scheduled_at)}</td><td><Link to={`/donors/${a.donor_id}`}>{a.donor_name}</Link></td>
                <td>{DONATION_TR[a.donation_type]}</td><td><Badge s={a.status} /></td>
                <td>{a.status === 'booked' && <ActButton onClick={async () => { await api.post(`/api/v1/appointments/${a.id}/cancel`); list.reload(); return 'Randevu iptal edildi'; }}>İptal</ActButton>}</td></tr>
            ))}
          </Table>
        )}
      </AsyncView>
    </>
  );
}
