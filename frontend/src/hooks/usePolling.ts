import { useEffect, useRef } from 'react';

export function usePolling(fn: () => void, intervalMs: number, enabled = true): void {
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    if (!enabled) return;
    const id = setInterval(() => fnRef.current(), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs, enabled]);
}
