import { useState, useEffect, useCallback } from 'react';
import { Loader2, Tv, Radio } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary } from '../api/client';
import { ShowCard } from '../components/ShowCard';
import { usePolling } from '../hooks/usePolling';

export function Dashboard() {
  const [shows, setShows] = useState<ShowSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const result = await api.getShows();
    if ('error' in result) {
      setError(result.error);
    } else {
      setShows(result.data);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);
  usePolling(load, 60_000);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 size={28} className="animate-spin" style={{ color: 'var(--emerald)' }} />
      </div>
    );
  }

  if (error) {
    return (
      <div
        className="rounded-xl p-6 text-center"
        style={{ background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)' }}
      >
        <p className="text-red-400 font-medium">{error}</p>
        <p className="text-sm mt-1" style={{ color: 'rgba(255,255,255,0.4)' }}>
          Check your API connection and settings.
        </p>
      </div>
    );
  }

  const active    = shows.filter((s) => s.status === 'Active');
  const stale     = shows.filter((s) => s.status === 'Stale');
  const completed = shows.filter((s) => s.status === 'Completed');

  if (shows.length === 0) {
    return (
      <div className="text-center py-24">
        <div
          className="inline-flex p-4 rounded-2xl mb-4"
          style={{ background: 'rgba(255,255,255,0.04)', border: '1px solid var(--border)' }}
        >
          <Tv size={32} style={{ color: 'rgba(255,255,255,0.2)' }} />
        </div>
        <h2 className="text-lg font-bold text-white/60">No shows tracked yet</h2>
        <p className="text-sm mt-2" style={{ color: 'rgba(255,255,255,0.3)' }}>
          Request a TV show via Seerr to get started.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      {/* Stats bar */}
      <div className="grid grid-cols-3 gap-3">
        {[
          { label: 'Active',    count: active.length,    color: 'var(--emerald)' },
          { label: 'Stale',     count: stale.length,     color: 'var(--amber)' },
          { label: 'Completed', count: completed.length,  color: 'var(--blue)' },
        ].map((stat) => (
          <div
            key={stat.label}
            className="rounded-xl p-4 text-center"
            style={{ background: 'var(--bg-card)', border: '1px solid var(--border)' }}
          >
            <div className="mono text-2xl font-semibold" style={{ color: stat.color }}>
              {stat.count}
            </div>
            <div className="text-xs mt-1" style={{ color: 'rgba(255,255,255,0.4)' }}>
              {stat.label}
            </div>
          </div>
        ))}
      </div>

      {active.length > 0 && (
        <section>
          <div className="flex items-center gap-2 mb-4">
            <Radio size={14} style={{ color: 'var(--emerald)' }} />
            <h2 className="text-sm font-semibold tracking-widest uppercase" style={{ color: 'var(--emerald)' }}>
              Active
            </h2>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {active.map((show, i) => (
              <ShowCard
                key={show.id}
                show={show}
                style={{ animationDelay: `${i * 60}ms` }}
              />
            ))}
          </div>
        </section>
      )}

      {stale.length > 0 && (
        <section>
          <h2 className="text-sm font-semibold tracking-widest uppercase mb-4" style={{ color: 'var(--amber)', opacity: 0.7 }}>
            Stale
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {stale.map((show, i) => (
              <ShowCard key={show.id} show={show} style={{ animationDelay: `${i * 60}ms`, opacity: 0.7 }} />
            ))}
          </div>
        </section>
      )}

      {completed.length > 0 && (
        <section>
          <h2 className="text-sm font-semibold tracking-widest uppercase mb-4" style={{ color: 'var(--blue)', opacity: 0.7 }}>
            Completed
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {completed.map((show, i) => (
              <ShowCard key={show.id} show={show} style={{ animationDelay: `${i * 60}ms`, opacity: 0.6 }} />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
