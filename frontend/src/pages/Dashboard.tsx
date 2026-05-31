import { useState, useEffect, useCallback, useMemo, useRef, useContext } from 'react';
import { Loader2, Tv } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary } from '../api/client';
import { ShowCard, buildUserColorMap } from '../components/ShowCard';
import { BackdropContext } from '../context/BackdropContext';
import { useSSE } from '../hooks/useSSE';

interface SectionProps {
  label: string;
  color: string;
  items: ShowSummary[];
  opacity?: number;
  colorMap: Record<string, string>;
  onReconcile: () => void;
  onHover: (s: ShowSummary) => void;
  onHoverEnd: () => void;
}

function Section({ label, color, items, opacity = 1, colorMap, onReconcile, onHover, onHoverEnd }: SectionProps) {
  if (items.length === 0) return null;
  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16 }}>
        <span style={{
          display: 'inline-block', width: 8, height: 8, borderRadius: '50%',
          background: color, boxShadow: `0 0 6px ${color}`,
        }} />
        <span className="overline" style={{ color, letterSpacing: '0.1em' }}>{label}</span>
      </div>
      <div className="show-grid" style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
        gap: 14, opacity,
        alignItems: 'stretch',
      }}>
        {items.map((show, i) => (
          <ShowCard
            key={show.tvdb_id}
            show={show}
            delay={i * 60}
            colorMap={colorMap}
            onReconcile={onReconcile}
            onHover={onHover}
            onHoverEnd={onHoverEnd}
          />
        ))}
      </div>
    </section>
  );
}

export function Dashboard() {
  const [shows, setShows] = useState<ShowSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { setBackdrop } = useContext(BackdropContext);
  const dashRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    try {
      const data = await api.getShows();
      setShows(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load shows');
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
    // 30s fallback polling in case SSE connection drops
    const interval = setInterval(load, 30_000);
    return () => clearInterval(interval);
  }, [load]);

  useSSE(load);

  const colorMap = useMemo(() => buildUserColorMap(shows), [shows]);

  const handleHover = useCallback((s: ShowSummary) => {
    setBackdrop(s.fanart_url || s.poster_url || null);
  }, [setBackdrop]);

  const handleHoverEnd = useCallback(() => {
    setBackdrop(null);
  }, [setBackdrop]);

  // Equalise all card heights to the tallest card per row
  useEffect(() => {
    const el = dashRef.current;
    if (!el) return;
    let busy = false;

    function equalise() {
      if (!el || busy) return;
      busy = true;
      const cards = [...el.querySelectorAll<HTMLElement>('.show-card')];
      cards.forEach((c) => { c.style.minHeight = ''; });
      requestAnimationFrame(() => requestAnimationFrame(() => {
        const max = cards.reduce((m, c) => Math.max(m, c.offsetHeight), 0);
        if (max > 0) cards.forEach((c) => { c.style.minHeight = max + 'px'; });
        requestAnimationFrame(() => { busy = false; });
      }));
    }

    const t = setTimeout(equalise, 400);
    window.addEventListener('resize', equalise);

    let ro: ResizeObserver | undefined;
    if (window.ResizeObserver) {
      ro = new ResizeObserver(equalise);
      el.querySelectorAll<HTMLElement>('.show-card').forEach((c) => ro!.observe(c));
    }

    return () => {
      clearTimeout(t);
      window.removeEventListener('resize', equalise);
      ro?.disconnect();
    };
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
        borderRadius: 'var(--radius-card)', padding: 24,
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

  const active   = shows.filter((s) => s.status === 'active');
  const inactive = shows.filter((s) => s.status === 'inactive');
  const removed  = shows.filter((s) => s.status === 'removed');

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

  return (
    <div ref={dashRef} className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 32 }}>
      {/* Stats */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
        {[
          { label: 'Active',   count: active.length,   color: 'var(--color-accent-green)' },
          { label: 'Inactive', count: inactive.length,  color: 'var(--color-accent-amber)' },
          { label: 'Removed',  count: removed.length,   color: 'var(--color-accent-danger, #ef4444)' },
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

      <Section label="Active"   color="var(--color-accent-green)"  items={active}   colorMap={colorMap} onReconcile={load} onHover={handleHover} onHoverEnd={handleHoverEnd} />
      <Section label="Inactive" color="var(--color-accent-amber)"  items={inactive} colorMap={colorMap} onReconcile={load} onHover={handleHover} onHoverEnd={handleHoverEnd} opacity={0.75} />
      <Section label="Removed"  color="#ef4444"                    items={removed}  colorMap={colorMap} onReconcile={load} onHover={handleHover} onHoverEnd={handleHoverEnd} opacity={0.6} />
    </div>
  );
}
