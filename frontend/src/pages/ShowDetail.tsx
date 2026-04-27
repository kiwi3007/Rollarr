import { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { ArrowLeft, Loader2, RefreshCw, UserX, Check } from 'lucide-react';
import { api } from '../api/client';
import type { ShowWithTrackers, EpisodeRow, TrackerWithUser } from '../api/client';
import { TrackerBadge } from '../components/TrackerBadge';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { WindowProgress } from '../components/WindowProgress';
import { usePolling } from '../hooks/usePolling';
import { useBackdrop } from '../context/BackdropContext';

const EP_STYLE: Record<EpisodeRow['status'], { color: string; label: string }> = {
  Monitored:   { color: 'var(--color-accent-orange)',    label: 'Buffered' },
  Unmonitored: { color: 'var(--color-text-muted)',       label: 'Upcoming' },
  Watched:     { color: 'rgba(255,255,255,0.3)',          label: 'Watched'  },
  Deleted:     { color: 'var(--color-accent-danger)',     label: 'Deleted'  },
};

const STATUS_BADGE: Record<string, { bg: string; border: string; color: string }> = {
  Active:    { bg: 'rgba(34,197,94,0.15)',  border: 'rgba(34,197,94,0.25)',  color: '#22c55e' },
  Stale:     { bg: 'rgba(245,158,11,0.15)', border: 'rgba(245,158,11,0.25)', color: '#f59e0b' },
  Completed: { bg: 'rgba(59,130,246,0.15)', border: 'rgba(59,130,246,0.25)', color: '#3b82f6' },
};

interface DropConfirm { showId: number; userId: number; username: string; }

export function ShowDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const showId = parseInt(id ?? '0', 10);

  const [show, setShow] = useState<ShowWithTrackers | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [dropConfirm, setDropConfirm] = useState<DropConfirm | null>(null);
  const { setBackdrop } = useBackdrop();

  const load = useCallback(async () => {
    const result = await api.getShow(showId);
    if ('error' in result) { setError(result.error); }
    else {
      setShow(result.data);
      setError(null);
      setBackdrop(result.data.backdrop_url);
    }
    setLoading(false);
  }, [showId, setBackdrop]);

  useEffect(() => { load(); }, [load]);
  usePolling(load, 60_000);

  async function handleRefresh() {
    setRefreshing(true);
    await api.refreshShow(showId);
    setTimeout(() => { setRefreshing(false); load(); }, 2000);
  }

  async function handleDrop() {
    if (!dropConfirm) return;
    await api.dropTracker(dropConfirm.showId, dropConfirm.userId);
    setDropConfirm(null);
    load();
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

  const episodesBySeason = show.episodes.reduce<Record<number, EpisodeRow[]>>((acc, ep) => {
    (acc[ep.season] ??= []).push(ep);
    return acc;
  }, {});

  const activeTrackers   = show.trackers.filter((t) => t.is_active === 1);
  const inactiveTrackers = show.trackers.filter((t) => t.is_active === 0);
  const s = STATUS_BADGE[show.status] ?? STATUS_BADGE.Stale;

  return (
    <>
      {dropConfirm && (
        <ConfirmDialog
          title="Drop Tracker"
          message={`Remove ${dropConfirm.username} from tracking this show? This may trigger a cleanup if they were the last active tracker.`}
          confirmLabel="Drop Tracker"
          onConfirm={handleDrop}
          onCancel={() => setDropConfirm(null)}
        />
      )}

      <div className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
        {/* Back button */}
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

        {/* Header: poster + title/meta */}
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 20 }}>
          {show.poster_url && (
            <img
              src={show.poster_url}
              alt={show.title}
              style={{
                width: 160, borderRadius: 'var(--radius-card)',
                border: '1px solid var(--color-glass-border)',
                flexShrink: 0, display: 'block',
                boxShadow: '0 8px 32px rgba(0,0,0,0.6)',
              }}
            />
          )}

          <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', justifyContent: 'space-between', alignSelf: 'stretch' }}>
            <div>
              <h1 style={{ fontSize: '1.5rem', fontWeight: 800, color: 'var(--color-text-primary)', lineHeight: 1.2, marginBottom: 10 }}>
                {show.title}
              </h1>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <span style={{
                  fontSize: '0.72rem', fontWeight: 700, padding: '2px 8px',
                  background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
                  borderRadius: 4, color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums',
                }}>
                  S{String(show.current_season).padStart(2, '0')}
                </span>
                <span style={{ fontSize: '0.75rem', color: 'var(--color-accent-orange)', fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>
                  E{show.current_window_start}–E{Math.min(show.current_window_start + show.buffer_size - 1, 999)} buffered
                </span>
                <span style={{
                  fontSize: '0.68rem', fontWeight: 700, padding: '2px 8px',
                  borderRadius: 'var(--radius-pill)', background: s.bg, border: `1px solid ${s.border}`, color: s.color,
                }}>
                  {show.status}
                </span>
              </div>
            </div>

            <button
              onClick={handleRefresh}
              disabled={refreshing}
              style={{
                display: 'inline-flex', alignItems: 'center', gap: 6,
                padding: '0 14px', height: 30, borderRadius: 'var(--radius-pill)',
                border: '1px solid var(--color-glass-border)',
                background: 'var(--color-glass-bg)', cursor: 'pointer',
                color: 'var(--color-text-secondary)', fontSize: '0.8rem', fontWeight: 600,
                alignSelf: 'flex-start',
              }}
            >
              <RefreshCw size={13} style={{ animation: refreshing ? 'spin 0.8s linear infinite' : 'none' }} />
              Refresh
            </button>
          </div>
        </div>

        {/* Trackers */}
        <div className="glass-card" style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: '14px 20px', borderBottom: '1px solid var(--color-glass-border)' }}>
            <span className="section-title">Trackers</span>
          </div>
          <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 8 }}>
            {activeTrackers.length === 0 && (
              <p style={{ color: 'var(--color-text-muted)', fontSize: '0.85rem' }}>No active trackers.</p>
            )}
            {activeTrackers.map((tracker) => (
              <ActiveTrackerRow
                key={tracker.id}
                tracker={tracker}
                onDrop={() => setDropConfirm({ showId: show.id, userId: tracker.user_id, username: tracker.plex_username })}
              />
            ))}
            {inactiveTrackers.length > 0 && (
              <div style={{ paddingTop: 12, borderTop: '1px solid var(--color-glass-border)', marginTop: 4 }}>
                <div className="overline" style={{ marginBottom: 8 }}>Inactive</div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  {inactiveTrackers.map((t) => <TrackerBadge key={t.id} tracker={t} />)}
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Episodes by season */}
        {Object.entries(episodesBySeason)
          .sort(([a], [b]) => Number(a) - Number(b))
          .map(([season, episodes]) => (
            <div key={season} className="glass-card" style={{ overflow: 'hidden', padding: 0 }}>
              <div style={{
                padding: '14px 20px', borderBottom: '1px solid var(--color-glass-border)',
                display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 20,
              }}>
                <span style={{ fontWeight: 700, fontSize: '0.9rem', color: 'var(--color-text-primary)' }}>
                  Season {season}
                </span>
                <div style={{ width: 200 }}>
                  <WindowProgress
                    totalEpisodes={episodes.length}
                    windowStart={Number(season) === show.current_season ? show.current_window_start : 1}
                    bufferSize={show.buffer_size}
                  />
                </div>
              </div>
              <table className="ep-table">
                <thead>
                  <tr>
                    <th>Ep</th>
                    <th>Status</th>
                    {activeTrackers.map((t) => <th key={t.id}>{t.plex_username}</th>)}
                  </tr>
                </thead>
                <tbody>
                  {episodes
                    .sort((a, b) => a.episode_number - b.episode_number)
                    .map((ep) => {
                      const st = EP_STYLE[ep.status] ?? EP_STYLE.Unmonitored;
                      return (
                        <tr key={ep.id}>
                          <td className="mono" style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }}>
                            E{String(ep.episode_number).padStart(2, '0')}
                          </td>
                          <td>
                            <span style={{ fontSize: '0.75rem', color: st.color, fontWeight: 600 }}>{st.label}</span>
                          </td>
                          {activeTrackers.map((tracker) => {
                            const watched = tracker.last_watched_season > Number(season)
                              || (tracker.last_watched_season === Number(season) && tracker.last_watched_episode >= ep.episode_number);
                            return (
                              <td key={tracker.id} style={{ textAlign: 'center' }}>
                                {watched
                                  ? <Check size={12} style={{ color: 'var(--color-accent-green)', display: 'block', margin: '0 auto' }} />
                                  : <span style={{ color: 'rgba(255,255,255,0.12)' }}>–</span>
                                }
                              </td>
                            );
                          })}
                        </tr>
                      );
                    })}
                </tbody>
              </table>
            </div>
          ))}
      </div>
    </>
  );
}

function ActiveTrackerRow({ tracker, onDrop }: { tracker: TrackerWithUser; onDrop: () => void }) {
  const daysSince = tracker.last_activity
    ? Math.floor((Date.now() - new Date(tracker.last_activity).getTime()) / 86400000)
    : null;

  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      padding: '10px 14px', borderRadius: 'var(--radius-inner)',
      background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{
          width: 28, height: 28, borderRadius: 'var(--radius-pill)',
          background: 'rgba(249,115,22,0.15)', border: '1px solid rgba(249,115,22,0.25)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: '0.7rem', fontWeight: 800, color: 'var(--color-accent-orange)',
        }}>
          {tracker.plex_username.charAt(0).toUpperCase()}
        </div>
        <div>
          <div style={{ fontWeight: 600, fontSize: '0.85rem', color: 'var(--color-text-primary)' }}>
            {tracker.plex_username}
          </div>
          <div style={{ fontSize: '0.68rem', color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>
            S{String(tracker.last_watched_season).padStart(2,'0')}E{String(tracker.last_watched_episode).padStart(2,'0')}
            {daysSince !== null && <> · {daysSince === 0 ? 'today' : `${daysSince}d ago`}</>}
          </div>
        </div>
      </div>
      <button
        onClick={onDrop}
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
        <UserX size={11} />
        Drop
      </button>
    </div>
  );
}
