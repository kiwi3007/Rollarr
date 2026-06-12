import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { Loader2, Search, Plus, Check, AlertTriangle } from 'lucide-react';
import { api } from '../api/client';
import type { PlexLibraryShow } from '../api/client';
import { ShowPoster } from '../components/ShowCard';
import { AddShowDialog } from '../components/AddShowDialog';
import { useSSE } from '../hooks/useSSE';

export function LibraryPage() {
  const navigate = useNavigate();
  const [shows, setShows] = useState<PlexLibraryShow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [adding, setAdding] = useState<PlexLibraryShow | null>(null);

  const load = useCallback(async () => {
    try {
      setShows(await api.getLibrary());
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load Plex library');
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);
  useSSE(load);

  const filtered = query
    ? shows.filter((s) => s.title.toLowerCase().includes(query.toLowerCase()))
    : shows;

  const addable = shows.filter((s) => s.in_sonarr && !s.is_tracked).length;

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
          Check your Plex connection and settings.
        </p>
      </div>
    );
  }

  return (
    <div className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Header + search */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: '1.15rem', fontWeight: 700, color: 'var(--color-text-primary)', margin: 0 }}>
            Plex Library
          </h2>
          <p style={{ fontSize: '0.78rem', color: 'var(--color-text-muted)', margin: '2px 0 0' }}>
            {shows.length} shows · {addable} available to add
          </p>
        </div>
        <span style={{ flex: 1 }} />
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '7px 12px', borderRadius: 'var(--radius-pill)',
          background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
          minWidth: 220,
        }}>
          <Search size={14} style={{ color: 'var(--color-text-muted)', flexShrink: 0 }} />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search shows…"
            style={{
              background: 'none', border: 'none', outline: 'none',
              color: 'var(--color-text-primary)', fontSize: '0.82rem', width: '100%',
            }}
          />
        </div>
      </div>

      {/* Grid */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))',
        gap: 12, alignItems: 'stretch',
      }}>
        {filtered.map((show) => {
          const disabled = !show.in_sonarr;
          return (
            <div
              key={show.plex_key}
              className="glass-card"
              style={{
                display: 'flex', gap: 12, padding: 12,
                opacity: disabled ? 0.55 : 1,
                cursor: show.is_tracked ? 'pointer' : 'default',
              }}
              onClick={() => { if (show.is_tracked) navigate(`/shows/${show.tvdb_id}`); }}
            >
              <div style={{ width: 56, flexShrink: 0, borderRadius: 8, overflow: 'hidden', position: 'relative' }}>
                <ShowPoster title={show.title} posterUrl={show.poster_url} />
              </div>
              <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 6 }}>
                <div style={{
                  fontWeight: 700, fontSize: '0.85rem', color: 'var(--color-text-primary)',
                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                }}>
                  {show.title}
                  {show.year > 0 && (
                    <span style={{ fontWeight: 500, color: 'var(--color-text-muted)', marginLeft: 6, fontSize: '0.75rem' }}>
                      {show.year}
                    </span>
                  )}
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                  {show.is_tracked ? (
                    <span style={{
                      display: 'inline-flex', alignItems: 'center', gap: 4,
                      fontSize: '0.65rem', fontWeight: 700, padding: '2px 8px',
                      borderRadius: 'var(--radius-pill)',
                      background: 'rgba(34,197,94,0.15)', border: '1px solid rgba(34,197,94,0.25)', color: '#22c55e',
                    }}>
                      <Check size={10} /> Tracked
                    </span>
                  ) : disabled ? (
                    <span
                      title="Rollarr can only manage shows Sonarr knows about"
                      style={{
                        display: 'inline-flex', alignItems: 'center', gap: 4,
                        fontSize: '0.65rem', fontWeight: 700, padding: '2px 8px',
                        borderRadius: 'var(--radius-pill)',
                        background: 'rgba(245,158,11,0.15)', border: '1px solid rgba(245,158,11,0.25)', color: '#f59e0b',
                      }}
                    >
                      <AlertTriangle size={10} /> Not in Sonarr
                    </span>
                  ) : (
                    <button
                      onClick={(e) => { e.stopPropagation(); setAdding(show); }}
                      style={{
                        display: 'inline-flex', alignItems: 'center', gap: 4,
                        padding: '3px 10px', borderRadius: 'var(--radius-pill)',
                        border: '1px solid rgba(249,115,22,0.3)',
                        background: 'rgba(249,115,22,0.12)',
                        color: 'var(--color-accent-orange)',
                        fontSize: '0.68rem', fontWeight: 700, cursor: 'pointer',
                        transition: 'all 0.15s',
                      }}
                    >
                      <Plus size={11} /> Add
                    </button>
                  )}
                </div>
              </div>
            </div>
          );
        })}
      </div>

      {filtered.length === 0 && (
        <p style={{ textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.85rem', padding: '40px 0' }}>
          No shows match “{query}”.
        </p>
      )}

      {adding && (
        <AddShowDialog
          show={adding}
          onClose={() => setAdding(null)}
          onAdded={load}
        />
      )}
    </div>
  );
}
