import { useState, useEffect, useCallback, useRef } from 'react';
import { Loader2, Radio, Tv } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary } from '../api/client';
import { ShowCard } from '../components/ShowCard';
import { usePolling } from '../hooks/usePolling';
import { useBackdrop } from '../context/BackdropContext';

export function Dashboard() {
  const [shows, setShows] = useState<ShowSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const dashRef = useRef<HTMLDivElement>(null);
  const { setBackdrop } = useBackdrop();

  const load = useCallback(async () => {
    const result = await api.getShows();
    if ('error' in result) {
      setError(result.error);
    } else {
      setShows(result.data);
      setError(null);
      // Pick a random backdrop from active shows
      const withBackdrop = result.data.filter((s) => s.backdrop_url && s.status === 'Active');
      if (withBackdrop.length > 0) {
        const pick = withBackdrop[Math.floor(Math.random() * withBackdrop.length)];
        setBackdrop(pick.backdrop_url);
      }
    }
    setLoading(false);
  }, [setBackdrop]);

  useEffect(() => { load(); }, [load]);
  usePolling(load, 60_000);

  // Equalise card heights across the entire grid
  useEffect(() => {
    function equalise() {
      if (!dashRef.current) return;
      const cards = [...dashRef.current.querySelectorAll<HTMLElement>('.show-card')];
      cards.forEach((c) => { c.style.minHeight = ''; });
      const max = cards.reduce((m, c) => Math.max(m, c.offsetHeight), 0);
      if (max > 0) cards.forEach((c) => { c.style.minHeight = `${max}px`; });
    }
    const t = setTimeout(equalise, 500);
    window.addEventListener('resize', equalise);
    return () => { clearTimeout(t); window.removeEventListener('resize', equalise); };
  }, [shows]);

  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 256 }}>
        <Loader2 size={28} style={{ animation: 'spin 0.8s linear infinite', color: 'var(--color-accent-orange)' }} />
      </div>
    );
  }

  if (error) {
    return (
      <div style={{
        borderRadius: 'var(--radius-card)', padding: '24px',
        background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)',
        textAlign: 'center',
      }}>
        <p style={{ color: '#fca5a5', fontWeight: 600 }}>{error}</p>
        <p style={{ fontSize: '0.85rem', marginTop: 4, color: 'var(--color-text-muted)' }}>
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
      <div style={{ textAlign: 'center', padding: '80px 0' }}>
        <div style={{
          display: 'inline-flex', padding: 16, borderRadius: 'var(--radius-inner)',
          background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
          marginBottom: 16, color: 'var(--color-text-muted)',
        }}>
          <Tv size={32} />
        </div>
        <h2 style={{ color: 'var(--color-text-muted)', fontWeight: 700, margin: '0 0 6px' }}>No shows tracked yet</h2>
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.85rem' }}>
          Request a TV show via Seerr to get started.
        </p>
      </div>
    );
  }

  function Section({
    label, color, items, opacity = 1,
  }: { label: string; color: string; items: ShowSummary[]; opacity?: number }) {
    if (items.length === 0) return null;
    return (
      <section>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16 }}>
          <Radio size={13} style={{ color }} />
          <span className="overline" style={{ color, letterSpacing: '0.1em' }}>{label}</span>
        </div>
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
          gap: 14, opacity,
          alignItems: 'stretch',
        }}>
          {items.map((show, i) => (
            <ShowCard key={show.id} show={show} delay={i * 60} />
          ))}
        </div>
      </section>
    );
  }

  return (
    <div ref={dashRef} className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 32 }}>
      {/* Stats */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
        {[
          { label: 'Active',    count: active.length,    color: 'var(--color-accent-green)' },
          { label: 'Stale',     count: stale.length,     color: 'var(--color-accent-amber)' },
          { label: 'Completed', count: completed.length,  color: 'var(--color-accent-blue)'  },
        ].map((stat) => (
          <div key={stat.label} className="stat-card">
            <div className="mono" style={{ fontSize: '2rem', fontWeight: 800, lineHeight: 1, color: stat.color }}>
              {stat.count}
            </div>
            <div style={{
              fontSize: '0.72rem', fontWeight: 700, letterSpacing: '0.1em',
              textTransform: 'uppercase', color: 'var(--color-text-muted)', marginTop: 6,
            }}>
              {stat.label}
            </div>
          </div>
        ))}
      </div>

      <Section label="Active"    color="var(--color-accent-green)"  items={active} />
      <Section label="Stale"     color="var(--color-accent-amber)"  items={stale}  opacity={0.75} />
      <Section label="Completed" color="var(--color-accent-blue)"   items={completed} opacity={0.6} />
    </div>
  );
}
