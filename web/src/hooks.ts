import { useCallback, useEffect, useRef, useState } from 'react';

export interface Async<T> {
  data?: T;
  error?: Error;
  loading: boolean;
  reload: () => void;
}

// useApi bir yükleyiciyi çalıştırır ve yükleniyor / veri / hata durumlarını döner.
// deps değişince yeniden yükler; interval verilirse arka planda yeniler (yükleniyor göstermeden).
export function useApi<T>(loader: () => Promise<T>, deps: unknown[] = [], interval?: number): Async<T> {
  const [state, setState] = useState<{ data?: T; error?: Error; loading: boolean }>({ loading: true });
  const [tick, setTick] = useState(0);
  const loaderRef = useRef(loader);
  loaderRef.current = loader;

  useEffect(() => {
    let alive = true;
    setState((s) => ({ ...s, loading: true, error: undefined }));
    loaderRef.current()
      .then((data) => alive && setState({ data, loading: false }))
      .catch((error: Error) => alive && setState({ error, loading: false }));
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  useEffect(() => {
    if (!interval) return;
    const t = setInterval(() => {
      loaderRef.current().then((data) => setState({ data, loading: false })).catch(() => { /* bir sonraki turda tekrar */ });
    }, interval);
    return () => clearInterval(t);
  }, [interval]);

  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { ...state, reload };
}

// useAction bir işlemi çalıştırırken düğmeyi kilitler, hatayı ve sonucu tutar.
export function useAction<A extends unknown[], R>(fn: (...args: A) => Promise<R>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [result, setResult] = useState<R>();
  const run = useCallback(async (...args: A) => {
    setBusy(true);
    setError(undefined);
    try {
      const r = await fn(...args);
      setResult(r);
      return r;
    } catch (e) {
      setError(e as Error);
      return undefined;
    } finally {
      setBusy(false);
    }
  }, [fn]);
  return { run, busy, error, result, setResult };
}
