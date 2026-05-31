import { useEffect, useRef } from 'react';

const TOKEN = (window as any).__ROLLARR_TOKEN__ ?? '';

export function useSSE(onRefresh: () => void): void {
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;

  useEffect(() => {
    const url = TOKEN ? `/api/events?token=${encodeURIComponent(TOKEN)}` : '/api/events';
    let es: EventSource;
    let reconnectTimer: ReturnType<typeof setTimeout>;
    let active = true;

    function connect() {
      if (!active) return;
      es = new EventSource(url);

      es.onmessage = (e) => {
        if (e.data === 'refresh') onRefreshRef.current();
      };

      es.onerror = () => {
        es.close();
        if (active) reconnectTimer = setTimeout(connect, 5000);
      };
    }

    connect();

    return () => {
      active = false;
      clearTimeout(reconnectTimer);
      es?.close();
    };
  }, []); // stable — url is module-level constant, callback accessed via ref
}
