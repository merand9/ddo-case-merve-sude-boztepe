// API istemcisi: JWT ekler, 401'de refresh token ile sessizce yeniler,
// durum değiştiren POST isteklerine Idempotency-Key ekler.

import type { Role } from './format';

export interface User {
  id: number; email: string; role: Role; full_name: string; hospital_id?: number | null; donor_id?: number | null;
}
export interface Session { access_token: string; refresh_token: string; expires_at: string; user: User }

export class ApiError extends Error {
  status: number;
  code?: string;
  details?: unknown;
  constructor(status: number, body: { error?: { code?: string; message?: string; details?: unknown } } | null) {
    super(body?.error?.message || `HTTP ${status}`);
    this.status = status;
    this.code = body?.error?.code;
    this.details = body?.error?.details;
  }
}

const KEY = 'db.session';
export const session = {
  get(): Session | null {
    try { return JSON.parse(sessionStorage.getItem(KEY) || 'null'); } catch { return null; }
  },
  set(s: Session) { try { sessionStorage.setItem(KEY, JSON.stringify(s)); } catch { /* yoksay */ } },
  clear() { try { sessionStorage.removeItem(KEY); } catch { /* yoksay */ } },
};

let onExpired: () => void = () => {};
export const setOnSessionExpired = (fn: () => void) => { onExpired = fn; };

const newKey = () => (crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}`);

interface Opts { headers?: Record<string, string>; idem?: boolean; retried?: boolean }

let refreshing: Promise<boolean> | null = null;
async function refresh(): Promise<boolean> {
  const s = session.get();
  if (!s?.refresh_token) return false;
  refreshing ??= (async () => {
    const r = await fetch('/auth/refresh', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ refresh_token: s.refresh_token }),
    });
    if (!r.ok) return false;
    session.set(await r.json());
    return true;
  })().finally(() => { refreshing = null; });
  return refreshing;
}

export async function request<T = unknown>(method: string, path: string, body?: unknown, opts: Opts = {}): Promise<T> {
  const s = session.get();
  const headers: Record<string, string> = { 'Content-Type': 'application/json', ...(opts.headers ?? {}) };
  if (s?.access_token) headers.Authorization = `Bearer ${s.access_token}`;
  if (method === 'POST' && opts.idem !== false) headers['Idempotency-Key'] = newKey();
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (res.status === 401 && s?.refresh_token && !opts.retried && !path.startsWith('/auth/')) {
    if (await refresh()) return request<T>(method, path, body, { ...opts, retried: true });
    session.clear();
    onExpired();
    throw new ApiError(401, { error: { message: 'Oturum süresi doldu, yeniden giriş yapın' } });
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new ApiError(res.status, data);
  return data as T;
}

export const api = {
  get: <T,>(path: string, headers?: Record<string, string>) => request<T>('GET', path, undefined, { headers }),
  post: <T,>(path: string, body: unknown = {}, opts?: Opts) => request<T>('POST', path, body, opts),
};

// ---- API tipleri (yalnızca ön yüzün kullandığı alanlar) ----
export interface Page<T> { items: T[]; next_cursor: number | null }
export interface Reason { code: string; message: string; until?: string; permanent?: boolean }
export interface Decision { eligible: boolean; reasons: Reason[]; next_eligible_at?: string | null; rule_version: string }
export interface Donor {
  id: number; donor_no: string; first_name: string; last_name: string; sex: 'M' | 'F'; birth_date: string; group: string; phone?: string;
}
export interface Deferral { id: number; reason_code: string; starts_on: string; ends_on?: string | null; permanent: boolean }
export interface Donation {
  id: number; donor_id: number; donation_type: string; collected_at: string; volume_ml: number; hemoglobin: number;
  bag_no: string; test_outcome: 'positive' | 'negative' | null; products: number;
}
export interface Appointment { id: number; donor_id: number; donor_name?: string; scheduled_at: string; donation_type: string; status: string }
export interface Product {
  id: number; product_no: string; barcode: string; parent_id?: number | null; component: string; abo: string; rh_positive: boolean;
  volume_ml: number; expires_at: string; status: string;
}
export interface StockRow {
  component: string; group: string; available: number; reserved: number; pending_test: number; quarantined: number;
  expiring_48h: number; alert?: 'critical' | 'low' | null;
}
export interface Reservation {
  id: number; product_no: string; group: string; status: string; hold_until: string; exact_match: boolean; product_expires_at: string;
}
export interface BloodRequest {
  id: number; hospital_name: string; component: string; group: string; units: number; urgency: string; patient_ref: string;
  status: string; created_at: string; reservations: Reservation[];
}
export interface TestOutcome { outcome: string; released: number; quarantined: unknown[]; recalls: unknown[]; lookback_donations: number[] }
export interface TestChange {
  id: number; donation_id: number; proposed: Record<string, boolean>; reason: string; requested_by: number; status: string;
}
