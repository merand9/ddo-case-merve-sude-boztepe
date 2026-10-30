import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, request, session } from './api';

const store = new Map<string, string>();
beforeEach(() => {
  store.clear();
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => store.set(k, v),
    removeItem: (k: string) => store.delete(k),
  });
});
afterEach(() => vi.unstubAllGlobals());

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });
const user = { id: 1, email: 'a@b', role: 'bank' as const, full_name: 'A' };

describe('api istemcisi', () => {
  it('POST isteklerine Idempotency-Key ve Bearer ekler', async () => {
    session.set({ access_token: 'T1', refresh_token: 'R1', expires_at: '', user });
    const fetch = vi.fn().mockResolvedValue(json(201, { id: 5 }));
    vi.stubGlobal('fetch', fetch);
    await request('POST', '/api/v1/requests', { a: 1 });
    const headers = fetch.mock.calls[0][1].headers;
    expect(headers.Authorization).toBe('Bearer T1');
    expect(headers['Idempotency-Key']).toBeTruthy();
  });

  it('401 alınca refresh token ile yenileyip isteği tekrarlar', async () => {
    session.set({ access_token: 'OLD', refresh_token: 'R1', expires_at: '', user });
    const fetch = vi.fn()
      .mockResolvedValueOnce(json(401, { error: { code: 'unauthorized', message: 'süre doldu' } }))
      .mockResolvedValueOnce(json(200, { access_token: 'NEW', refresh_token: 'R2', expires_at: '', user }))
      .mockResolvedValueOnce(json(200, [1, 2]));
    vi.stubGlobal('fetch', fetch);
    await expect(request('GET', '/api/v1/stock')).resolves.toEqual([1, 2]);
    expect(fetch.mock.calls[1][0]).toBe('/auth/refresh');
    expect(fetch.mock.calls[2][1].headers.Authorization).toBe('Bearer NEW');
    expect(session.get()?.refresh_token).toBe('R2');
  });

  it('hata gövdesini ApiError olarak taşır', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(422, { error: { code: 'donor_ineligible', message: 'uygun değil', details: { reasons: [] } } })));
    const err = (await request('POST', '/api/v1/donations', {}).catch((e: unknown) => e)) as ApiError;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(422);
    expect(err.code).toBe('donor_ineligible');
  });

  it('refresh başarısızsa oturumu temizler', async () => {
    session.set({ access_token: 'OLD', refresh_token: 'R1', expires_at: '', user });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(401, { error: { message: 'x' } })));
    await expect(request('GET', '/api/v1/stock')).rejects.toBeInstanceOf(ApiError);
    expect(session.get()).toBeNull();
  });
});
