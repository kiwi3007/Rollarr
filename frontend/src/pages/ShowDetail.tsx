import { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { ArrowLeft, Loader2, RefreshCw, Trash2 } from 'lucide-react';
import { api } from '../api/client';
import type { ShowDetail as ShowDetailType, UserRequest, Flag } from '../api/client';

const STATUS_BADGE: Record<string, { bg: string; border: string; color: string }> = {
  active:   { bg: 'rgba(34,197,94,0.15)',  border: 'rgba(34,197,94,0.25)',  color: '#22c55e' },
  inactive: { bg: 'rgba(245,158,11,0.15)', border: 'rgba(245,158,11,0.25)', color: '#f59e0b' },
  removed:  { bg: 'rgba(239,68,68,0.15)',  border: 'rgba(239,68,68,0.25)',  color: '#ef4444' },
};

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

export function ShowDetail() {
  const { tvdbId } = useParams<{ tvdbId: string }>();
  const navigate = useNavigate();
  const tvdbIdNum = parseInt(tvdbId ?? '0', 10);

  const [show, setShow] = useState<ShowDetailType | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reconciling, setReconciling] = useState(false);
  const [reconcileToast, setReconcileToast] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await api.getShow(tvdbIdNum);
      setShow(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load show');
    }
    setLoading(false);
  }, [tvdbIdNum]);

  useEffect(() => { load(); }, [load]);

  async function handleReconcile() {
    setReconciling(true);
    try {
      await api.reconcileShow(tvdbIdNum);
      setReconcileToast('Reconcile queued');
    } catch {
      setReconcileToast('Reconcile failed');
    }
    setTimeout(() => { setReconciling(false); setReconcileToast(null); }, 2500);
  }

  async function handleDeleteRequest(req: UserRequest) {
    try {
      await api.deleteRequest(tvdbIdNum, req.plex_user_id);
      load();
    } catch {
      // silently refresh
      load();
    }
  }

  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 256 }}>
        <Loader2 size={28} style={{ animation: 'spin 0.8s linear infinite', color: 'var(--color-accent-orange)' }} />
      </div>
    );
  }

  if (error || !show) {
    return (
      <div style={{ borderRadius: 'var(--radius-card)', padding: 24, background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)', textAlign: 'center' }}>
        <p style={{ color: '#fca5a5', fontWeight: 600 }}>{error ?? 'Show not found'}</p>
      </div>
    );
  }

  const s = STATUS_BADGE[show.status] ?? STATUS_BADGE.inactive;
  const requests = show.requests ?? [];
  const openFlags = show.open_flags ?? [];
  const expectedState = show.expected_state ?? {};
  const allEpisodes = show.all_episodes ?? {};
  // Only show seasons that are actively managed (appear in expected_state)
  const seasons = Object.keys(expectedState).map(Number).sort((a, b) => a - b);

  return (
    <div className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Hero header — fanart background if available */}
      <div style={{
        position: 'relative',
        borderRadius: 'var(--radius-card)',
        overflow: 'hidden',
        minHeight: show.fanart_url ? 180 : 'auto',
      }}>
        {/* Fanart background */}
        {show.fanart_url && (
          <div style={{
            position: 'absolute', inset: 0,
            backgroundImage: `url(${show.fanart_url})`,
            backgroundSize: 'cover', backgroundPosition: 'center top',
            filter: 'brightness(0.35) saturate(0.8)',
          }} />
        )}

        {/* Gradient overlay so text is always readable */}
        {show.fanart_url && (
          <div style={{
            position: 'absolute', inset: 0,
            background: 'linear-gradient(to right, rgba(10,10,15,0.85) 0%, rgba(10,10,15,0.4) 60%, transparent 100%)',
          }} />
        )}

        {/* Content */}
        <div style={{
          position: 'relative',
          display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between',
          gap: 16, flexWrap: 'wrap',
          padding: show.fanart_url ? '20px 20px 20px 20px' : '0',
        }}>
          {/* Left: back + title */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <button
              onClick={() => navigate('/')}
              style={{
                width: 32, height: 32, borderRadius: 'var(--radius-inner)',
                border: '1px solid var(--color-glass-border)',
                background: 'var(--color-glass-bg-light)',
                cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
                color: 'var(--color-text-muted)', alignSelf: 'flex-start',
              }}
            >
              <ArrowLeft size={15} />
            </button>
            <div>
              <h1 style={{ fontSize: '1.5rem', fontWeight: 800, color: 'var(--color-text-primary)', lineHeight: 1.2, marginBottom: 10 }}>
                {show.title}
              </h1>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <span style={{
                  fontSize: '0.68rem', fontWeight: 700, padding: '2px 8px',
                  borderRadius: 'var(--radius-pill)', background: s.bg, border: `1px solid ${s.border}`, color: s.color,
                }}>
                  {show.status}
                </span>
                <span style={{
                  fontSize: '0.72rem', color: 'var(--color-text-muted)',
                  background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
                  padding: '2px 8px', borderRadius: 4, fontVariantNumeric: 'tabular-nums',
                }}>
                  Buffer: {show.effective_buffer_size} episodes
                </span>
                <span style={{
                  fontSize: '0.72rem', color: 'var(--color-text-muted)',
                  background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
                  padding: '2px 8px', borderRadius: 4,
                }}>
                  {show.active_request_count} active request{show.active_request_count !== 1 ? 's' : ''}
                </span>
              </div>
            </div>
          </div>

          {/* Right: poster + reconcile */}
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
            {show.poster_url && (
              <img
                src={show.poster_url}
                alt={show.title}
                style={{
                  width: 72, borderRadius: 6, boxShadow: '0 4px 16px rgba(0,0,0,0.5)',
                  display: 'block',
                }}
              />
            )}
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 8 }}>
              {reconcileToast && (
                <span style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>{reconcileToast}</span>
              )}
              <button
                onClick={handleReconcile}
                disabled={reconciling}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: 6,
                  padding: '0 14px', height: 32, borderRadius: 'var(--radius-pill)',
                  border: '1px solid var(--color-glass-border)',
                  background: 'var(--color-glass-bg)', cursor: reconciling ? 'not-allowed' : 'pointer',
                  color: 'var(--color-text-secondary)', fontSize: '0.8rem', fontWeight: 600,
                  opacity: reconciling ? 0.6 : 1,
                }}
              >
                <RefreshCw size={13} style={{ animation: reconciling ? 'spin 0.8s linear infinite' : 'none' }} />
                Reconcile Now
              </button>
            </div>
          </div>
        </div>
      </div>

      {/* Section 1: User Requests */}
      <div className="glass-card" style={{ padding: 0, overflow: 'hidden' }}>
        <div style={{ padding: '14px 20px', borderBottom: '1px solid var(--color-glass-border)' }}>
          <span className="section-title">User Requests</span>
        </div>
        {requests.length === 0 ? (
          <div style={{ padding: '20px', color: 'var(--color-text-muted)', fontSize: '0.85rem' }}>
            No user requests.
          </div>
        ) : (
          <table className="ep-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>User</th>
                <th>Progress</th>
                <th>Buffer window</th>
                <th>Requested At</th>
                <th>Rewatching</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {requests.map((req) => {
                const s = req.last_watched_season;
                const e = req.last_watched_episode;
                const watched = s != null && e != null
                  ? `S${String(s).padStart(2,'0')}E${String(e).padStart(2,'0')}`
                  : null;
                const nextEp = e != null ? e + 1 : 1;
                const nextSeason = s != null ? s : 1;
                const bufEnd = nextEp + show.effective_buffer_size - 1;
                const bufferLabel = watched
                  ? `S${String(nextSeason).padStart(2,'0')}E${String(nextEp).padStart(2,'0')}–E${String(bufEnd).padStart(2,'0')}`
                  : `S01E01–E${String(show.effective_buffer_size).padStart(2,'0')}`;
                return (
                <tr key={req.plex_user_id}>
                  <td style={{ fontSize: '0.8rem', color: 'var(--color-text-secondary)', fontWeight: 600 }}>
                    {req.display_name || req.plex_user_id}
                  </td>
                  <td>
                    {watched ? (
                      <span style={{
                        fontSize: '0.75rem', fontWeight: 700,
                        fontVariantNumeric: 'tabular-nums',
                        color: 'var(--color-accent-green)',
                      }}>
                        {watched}
                      </span>
                    ) : (
                      <span style={{ color: 'var(--color-text-muted)', fontSize: '0.8rem' }}>Not started</span>
                    )}
                  </td>
                  <td>
                    <span style={{
                      fontSize: '0.72rem', fontWeight: 600,
                      fontVariantNumeric: 'tabular-nums',
                      color: 'var(--color-accent-orange)',
                      background: 'rgba(249,115,22,0.1)',
                      border: '1px solid rgba(249,115,22,0.25)',
                      padding: '2px 7px', borderRadius: 4,
                    }}>
                      {bufferLabel}
                    </span>
                  </td>
                  <td style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>
                    {formatDate(req.request_timestamp)}
                  </td>
                  <td>
                    {req.is_rewatching ? (
                      <span style={{
                        fontSize: '0.68rem', fontWeight: 700, padding: '2px 8px',
                        borderRadius: 'var(--radius-pill)',
                        background: 'rgba(139,92,246,0.15)', border: '1px solid rgba(139,92,246,0.3)',
                        color: '#a78bfa',
                      }}>
                        Rewatching
                      </span>
                    ) : (
                      <span style={{ color: 'var(--color-text-muted)', fontSize: '0.8rem' }}>—</span>
                    )}
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    <button
                      onClick={() => handleDeleteRequest(req)}
                      style={{
                        display: 'inline-flex', alignItems: 'center', gap: 4,
                        padding: '4px 10px', borderRadius: 'var(--radius-pill)',
                        border: '1px solid transparent', background: 'transparent',
                        color: 'var(--color-text-muted)', fontSize: '0.75rem', fontWeight: 600,
                        cursor: 'pointer', transition: 'all 0.15s',
                      }}
                      onMouseEnter={(e) => {
                        (e.currentTarget as HTMLButtonElement).style.background = 'rgba(239,68,68,0.12)';
                        (e.currentTarget as HTMLButtonElement).style.color = '#fca5a5';
                        (e.currentTarget as HTMLButtonElement).style.borderColor = 'rgba(239,68,68,0.25)';
                      }}
                      onMouseLeave={(e) => {
                        (e.currentTarget as HTMLButtonElement).style.background = 'transparent';
                        (e.currentTarget as HTMLButtonElement).style.color = 'var(--color-text-muted)';
                        (e.currentTarget as HTMLButtonElement).style.borderColor = 'transparent';
                      }}
                    >
                      <Trash2 size={11} />
                      Delete
                    </button>
                  </td>
                </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* Section 2: Expected State */}
      <div className="glass-card" style={{ padding: 0, overflow: 'hidden' }}>
        <div style={{ padding: '14px 20px', borderBottom: '1px solid var(--color-glass-border)' }}>
          <span className="section-title">Expected State</span>
        </div>
        {seasons.length === 0 ? (
          <div style={{ padding: '20px', color: 'var(--color-text-muted)', fontSize: '0.85rem' }}>
            No expected state computed yet.
          </div>
        ) : (
          <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>
            {seasons.map((season) => {
              const buffered = new Set(expectedState[season] ?? []);
              const allEps = allEpisodes[season] ?? [];
              // Merge: all known eps + any buffered eps not yet in Sonarr data
              const epSet = new Set([...allEps, ...(expectedState[season] ?? [])]);
              const epList = Array.from(epSet).sort((a, b) => a - b);
              return (
                <div key={season} style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
                  <span style={{
                    flexShrink: 0, width: 64,
                    fontSize: '0.75rem', fontWeight: 700, color: 'var(--color-text-muted)',
                    fontVariantNumeric: 'tabular-nums', paddingTop: 2,
                  }}>
                    Season {String(season).padStart(2, '0')}
                  </span>
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                    {epList.map((ep) => {
                      const inBuffer = buffered.has(ep);
                      return (
                        <span
                          key={ep}
                          style={{
                            padding: '2px 7px', borderRadius: 4,
                            background: inBuffer ? 'rgba(249,115,22,0.15)' : 'rgba(255,255,255,0.04)',
                            border: `1px solid ${inBuffer ? 'rgba(249,115,22,0.3)' : 'rgba(255,255,255,0.08)'}`,
                            fontSize: '0.68rem', fontWeight: 700,
                            color: inBuffer ? 'var(--color-accent-orange)' : 'var(--color-text-muted)',
                            fontVariantNumeric: 'tabular-nums',
                            opacity: inBuffer ? 1 : 0.5,
                          }}
                        >
                          E{String(ep).padStart(2, '0')}
                        </span>
                      );
                    })}
                    {epList.length === 0 && (
                      <span style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>None</span>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Section 3: Open Flags */}
      <div className="glass-card" style={{ padding: 0, overflow: 'hidden' }}>
        <div style={{ padding: '14px 20px', borderBottom: '1px solid var(--color-glass-border)' }}>
          <span className="section-title">Open Flags</span>
        </div>
        {openFlags.length === 0 ? (
          <div style={{ padding: '20px', color: 'var(--color-text-muted)', fontSize: '0.85rem' }}>
            No open flags.
          </div>
        ) : (
          <table className="ep-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Episode ID</th>
                <th>Description</th>
                <th>Status</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {openFlags.map((flag) => (
                <OpenFlagRow key={flag.id} flag={flag} />
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function OpenFlagRow({ flag }: { flag: Flag }) {
  return (
    <tr>
      <td className="mono" style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
        {flag.sonarr_episode_id ?? '—'}
      </td>
      <td style={{ fontSize: '0.8rem', color: 'var(--color-text-secondary)' }}>
        {flag.issue_description}
      </td>
      <td>
        <span style={{
          fontSize: '0.68rem', fontWeight: 700, padding: '2px 8px',
          borderRadius: 'var(--radius-pill)',
          background: flag.status === 'open' ? 'rgba(239,68,68,0.15)' : 'rgba(34,197,94,0.15)',
          border: `1px solid ${flag.status === 'open' ? 'rgba(239,68,68,0.3)' : 'rgba(34,197,94,0.3)'}`,
          color: flag.status === 'open' ? '#fca5a5' : '#22c55e',
        }}>
          {flag.status}
        </span>
      </td>
      <td style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', whiteSpace: 'nowrap' }}>
        {new Date(flag.created_at).toLocaleDateString()}
      </td>
    </tr>
  );
}
