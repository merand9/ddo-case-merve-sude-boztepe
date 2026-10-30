import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import { api, session, setOnSessionExpired, type Session, type User } from './api';

interface AuthCtx {
  user: User | null;
  login: (email: string, password: string) => Promise<User>;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(() => session.get()?.user ?? null);

  useEffect(() => setOnSessionExpired(() => setUser(null)), []);

  const login = useCallback(async (email: string, password: string) => {
    const s = await api.post<Session>('/auth/login', { email, password }, { idem: false });
    session.set(s);
    setUser(s.user);
    return s.user;
  }, []);

  const logout = useCallback(async () => {
    const s = session.get();
    try { if (s) await api.post('/auth/logout', { refresh_token: s.refresh_token }, { idem: false }); } catch { /* yoksay */ }
    session.clear();
    setUser(null);
  }, []);

  return <Ctx.Provider value={{ user, login, logout }}>{children}</Ctx.Provider>;
}

export function useAuth() {
  const c = useContext(Ctx);
  if (!c) throw new Error('AuthProvider eksik');
  return c;
}
