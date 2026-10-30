import { useRef } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth';
import { Field, Form } from '../components/ui';
import { DEMO_ACCOUNTS } from '../format';
import { HOME } from '../routes';

export default function Login() {
  const { user, login } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  const email = useRef<HTMLInputElement>(null);
  const pw = useRef<HTMLInputElement>(null);
  if (user) return <Navigate to={HOME[user.role]} replace />;
  const from = (loc.state as { from?: string } | null)?.from;
  return (
    <div className="login">
      <div className="card">
        <h1>DamarBağış</h1>
        <p className="sub">Bölge kan merkezi: bağışçı kabulü, test, üretim, stok ve hastane talepleri.</p>
        <Form className="grid login-form" submit="Giriş yap" onSubmit={async (v) => {
          const u = await login(v.email, v.password);
          nav(from && from !== '/login' ? from : HOME[u.role], { replace: true });
        }}>
          <Field label="E-posta"><input ref={email} name="email" type="email" autoComplete="username" required /></Field>
          <Field label="Parola"><input ref={pw} name="password" type="password" autoComplete="current-password" required /></Field>
        </Form>
      </div>
      <div className="card demo-list">
        <h3>Demo hesapları</h3>
        <p className="sub">Parola: <span className="mono">Demo1234!</span> — tıklayınca form dolar.</p>
        {DEMO_ACCOUNTS.map(([e, r]) => (
          <button key={e} type="button" onClick={() => {
            if (email.current && pw.current) { email.current.value = e; pw.current.value = 'Demo1234!'; pw.current.focus(); }
          }}><span>{r}</span><small>{e}</small></button>
        ))}
      </div>
    </div>
  );
}
