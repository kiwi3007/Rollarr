import { useEffect, useRef } from 'react';
import { getToken } from '../auth';

export function useSSE(onRefresh: () => void): void {
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;

  useEffect(() => {
    let es: EventSource;
    let reconnectTimer: ReturnType<typeof setTimeout>;
    let active = true;

    function connect() {
      if (!active) return;
      // Read the token on every connect so a sign-in takes effect on reconnect.
      const token = getToken();
      const url = token ? `/api/events?token=${encodeURIComponent(token)}` : '/api/events';
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
  }, []); // stable — token read per connect, callback accessed via ref
}
