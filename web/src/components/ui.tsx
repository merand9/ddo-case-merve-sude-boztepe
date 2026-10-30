import { createContext, useCallback, useContext, useState, type ReactNode, type FormEvent } from 'react';
import { ApiError, type Decision, type StockRow } from '../api';
import { COMP_TR, COMPONENTS, GROUPS, STATUS_TR, fmtDate } from '../format';
import type { Async } from '../hooks';

// ---------- rozetler ----------
const BADGE: Record<string, string> = {
  available: 'b-ok', approved: 'b-ok', completed: 'b-ok', negative: 'b-ok',
  issued: 'b-info', transfused: 'b-info', booked: 'b-info',
  reserved: 'b-warn', active: 'b-warn', partial: 'b-warn', pending_test: 'b-warn', pending: 'b-warn', open: 'b-warn',
  quarantined: 'b-bad', discarded: 'b-bad', expired: 'b-bad', rejected: 'b-bad', preempted: 'b-bad', positive: 'b-bad',
};
export const Badge = ({ s, label }: { s: string; label?: string }) => (
  <span className={`badge ${BADGE[s] ?? ''}`}>{label ?? STATUS_TR[s] ?? s}</span>
);
export const Urgency = ({ u }: { u: string }) =>
  u === 'emergency' ? <span className="badge b-bad">Çok acil</span> : u === 'urgent' ? <span className="badge b-warn">Acil</span> : <>Rutin</>;
export const Grp = ({ g }: { g: string }) => <span className="grp">{g}</span>;

// ---------- durumlar ----------
export function ErrorBox({ error }: { error: Error }) {
  const e = error as ApiError;
  const d = e.details as { reasons?: { message: string; until?: string }[] } | string[] | undefined;
  return (
    <div className="state error" role="alert">
      <b>Hata{e.status ? ` (${e.status})` : ''}:</b> {e.message}
      {Array.isArray(d) && <ul className="reasons">{d.map((x) => <li key={x}>{x}</li>)}</ul>}
      {d && !Array.isArray(d) && d.reasons && (
        <ul className="reasons">{d.reasons.map((r, i) => <li key={i}>{r.message}{r.until ? ` (uygun: ${fmtDate(r.until)})` : ''}</li>)}</ul>
      )}
    </div>
  );
}
export const Loading = ({ rows = 4 }: { rows?: number }) => (
  <div className="card" aria-busy="true">{Array.from({ length: rows }, (_, i) => <div key={i} className="skeleton" />)}</div>
);
export const Empty = ({ children }: { children: ReactNode }) => <div className="state">{children}</div>;

// AsyncView ilk yüklemede iskelet, hata, boş ya da içerik gösterir. Yenileme sırasında eski içerik görünür kalır.
export function AsyncView<T>({ state, empty, isEmpty, children }: {
  state: Async<T>; empty?: ReactNode; isEmpty?: (d: T) => boolean; children: (d: T) => ReactNode;
}) {
  if (state.error && state.data === undefined) return <ErrorBox error={state.error} />;
  if (state.data === undefined) return <Loading />;
  const d = state.data;
  const blank = isEmpty ? isEmpty(d)
    : Array.isArray(d) ? d.length === 0
      : (d as { items?: unknown[] })?.items?.length === 0;
  return <>{state.error && <ErrorBox error={state.error} />}{blank ? <Empty>{empty ?? 'Kayıt yok'}</Empty> : children(d)}</>;
}

// ---------- düzen ----------
export const PageHead = ({ title, sub, children }: { title: string; sub?: ReactNode; children?: ReactNode }) => (
  <div className="page-head"><div><h1>{title}</h1>{sub && <p className="sub">{sub}</p>}</div>{children}</div>
);
export const Card = ({ title, children, className = '' }: { title?: ReactNode; children: ReactNode; className?: string }) => (
  <section className={`card ${className}`}>{title && <h2>{title}</h2>}{children}</section>
);
export const Field = ({ label, children, grow }: { label: string; children: ReactNode; grow?: boolean }) => (
  <label className="field" style={grow ? { flex: 1 } : undefined}><span>{label}</span>{children}</label>
);
export const Notice = ({ kind, children }: { kind: 'ok' | 'bad' | 'warn' | 'info'; children: ReactNode }) => (
  <div className={`notice ${kind}`}>{children}</div>
);
export const Table = ({ head, children }: { head: ReactNode[]; children: ReactNode }) => (
  <div className="table-wrap"><table><thead><tr>{head.map((h, i) => <th key={i}>{h}</th>)}</tr></thead><tbody>{children}</tbody></table></div>
);

// Form: gönderim sırasında düğmeyi kilitler, hatayı ya da başarı mesajını altında gösterir.
export function Form({ onSubmit, children, className, submit, primary = true }: {
  onSubmit: (data: Record<string, string>) => Promise<ReactNode | void>; children: ReactNode; className?: string;
  submit: string; primary?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [msg, setMsg] = useState<ReactNode>();
  const handle = async (ev: FormEvent<HTMLFormElement>) => {
    ev.preventDefault();
    const data = Object.fromEntries(new FormData(ev.currentTarget)) as Record<string, string>;
    setBusy(true); setError(undefined); setMsg(undefined);
    try { setMsg((await onSubmit(data)) ?? undefined); } catch (e) { setError(e as Error); } finally { setBusy(false); }
  };
  return (
    <form onSubmit={handle} className={className}>
      {children}
      <div className="form-actions"><button type="submit" className={primary ? 'primary' : ''} disabled={busy}>{busy ? 'Gönderiliyor…' : submit}</button></div>
      {error && <ErrorBox error={error} />}
      {msg && (typeof msg === 'string' ? <Notice kind="ok">{msg}</Notice> : msg)}
    </form>
  );
}

// ---------- bildirimler ----------
type Toast = { id: number; msg: string; bad: boolean };
const ToastCtx = createContext<(msg: string, bad?: boolean) => void>(() => {});
export function ToastProvider({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const push = useCallback((msg: string, bad = false) => {
    const id = Date.now() + Math.random();
    setList((l) => [...l, { id, msg, bad }]);
    setTimeout(() => setList((l) => l.filter((t) => t.id !== id)), 4500);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div id="toast" role="status" aria-live="polite">{list.map((t) => <div key={t.id} className={t.bad ? 'bad' : ''}>{t.msg}</div>)}</div>
    </ToastCtx.Provider>
  );
}
export const useToast = () => useContext(ToastCtx);

// ActButton tablo içi eylem: istek süresince kilitlenir, sonucu bildirim olarak gösterir.
export function ActButton({ onClick, children, primary, confirm: ask }: {
  onClick: () => Promise<string | void>; children: ReactNode; primary?: boolean; confirm?: string;
}) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  return (
    <button className={`small ${primary ? 'primary' : ''}`} disabled={busy} onClick={async () => {
      if (ask && !window.confirm(ask)) return;
      setBusy(true);
      try { const m = await onClick(); if (m) toast(m); } catch (e) { toast((e as Error).message, true); } finally { setBusy(false); }
    }}>{children}</button>
  );
}

// ---------- alana özgü ----------
export function StockMatrix({ rows }: { rows: StockRow[] }) {
  return (
    <div className="matrix" role="table" aria-label="Stok matrisi">
      <div className="h rh" />
      {GROUPS.map((g) => <div key={g} className="h grp">{g}</div>)}
      {COMPONENTS.map((c) => [
        <div key={c} className="h rh">{COMP_TR[c]}</div>,
        ...GROUPS.map((g) => {
          const r = rows.find((x) => x.component === c && x.group === g);
          return (
            <div key={c + g} className={`cell ${r?.alert ?? ''}`}
              title={`Rezerve ${r?.reserved ?? 0} · Test bekleyen ${r?.pending_test ?? 0} · 48 saatte dolacak ${r?.expiring_48h ?? 0}`}>
              <b>{r?.available ?? 0}</b><small>{r?.reserved ? `+${r.reserved} rez.` : ' '}</small>
            </div>
          );
        }),
      ])}
    </div>
  );
}

export function DecisionBox({ d }: { d: Decision }) {
  if (d.eligible) return <Notice kind="ok"><b>Uygun.</b> Kural sürümü {d.rule_version}.</Notice>;
  return (
    <Notice kind="bad">
      <b>Uygun değil.</b>{' '}
      {d.next_eligible_at ? <>En erken: <b>{fmtDate(d.next_eligible_at)}</b>.</> : 'Kalıcı ya da ölçüm kaynaklı red.'}
      <ul className="reasons">
        {d.reasons.map((r, i) => <li key={i}><span className="mono">{r.code}</span> — {r.message}{r.until ? ` (bitiş ${fmtDate(r.until)})` : ''}</li>)}
      </ul>
      <small>Kural sürümü {d.rule_version || '—'}</small>
    </Notice>
  );
}

export const GroupSelect = ({ name = 'group', all, defaultValue }: { name?: string; all?: string; defaultValue?: string }) => (
  <select name={name} defaultValue={defaultValue}>
    {all !== undefined && <option value="">{all}</option>}
    {GROUPS.map((g) => <option key={g}>{g}</option>)}
  </select>
);
export const ComponentSelect = ({ all }: { all?: boolean }) => (
  <select name="component">
    {all && <option value="">Tümü</option>}
    {COMPONENTS.map((c) => <option key={c} value={c}>{COMP_TR[c]}</option>)}
  </select>
);
