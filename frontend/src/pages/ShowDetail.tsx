import { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { ArrowLeft, Loader2, RefreshCw, UserX, Check, Circle, Trash2, EyeOff } from 'lucide-react';
import { api } from '../api/client';
import type { ShowWithTrackers, EpisodeRow, TrackerWithUser } from '../api/client';
import { TrackerBadge } from '../components/TrackerBadge';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { WindowProgress } from '../components/WindowProgress';
import { usePolling } from '../hooks/usePolling';

const EP_STATUS_STYLES: Record<EpisodeRow['status'], { color: string; icon: React.ReactNode; label: string }> = {
  Monitored:   { color: '#6ee7b7', icon: <Circle size={10} fill="#10b981" />,    label: 'Buffered' },
  Unmonitored: { color: 'rgba(255,255,255,0.25)', icon: <EyeOff size={10} />,   label: 'Upcoming' },
  Watched:     { color: 'rgba(255,255,255,0.35)', icon: <Check size={10} />,     label: 'Watched' },
  Deleted:     { color: 'rgba(255,255,255,0.2)',  icon: <Trash2 size={10} />,    label: 'Deleted' },
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

  const load = useCallback(async () => {
    const result = await api.getShow(showId);
    if ('error' in result) { setError(result.error); }
    else { setShow(result.data); setError(null); }
    setLoading(false);
  }, [showId]);

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
      <div className="flex items-center justify-center h-64">
        <Loader2 size={28} className="animate-spin" style={{ color: 'var(--emerald)' }} />
      </div>
    );
  }

  if (error || !show) {
    return (
      <div className="rounded-xl p-6 text-center" style={{ background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)' }}>
        <p className="text-red-400 font-medium">{error ?? 'Show not found'}</p>
      </div>
    );
  }

  // Group episodes by season
  const episodesBySeason = show.episodes.reduce<Record<number, EpisodeRow[]>>((acc, ep) => {
    (acc[ep.season] ??= []).push(ep);
    return acc;
  }, {});

  const activeTrackers = show.trackers.filter((t) => t.is_active);
  const inactiveTrackers = show.trackers.filter((t) => !t.is_active);

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

      <div className="space-y-6 fade-up">
        {/* Header */}
        <div className="flex items-start gap-4">
          <button
            onClick={() => navigate('/')}
            className="p-2 rounded-lg transition-colors mt-0.5"
            style={{ background: 'rgba(255,255,255,0.06)', color: 'rgba(255,255,255,0.5)' }}
          >
            <ArrowLeft size={16} />
          </button>
          <div className="flex-1 min-w-0">
            <h1 className="text-2xl font-extrabold text-white truncate">{show.title}</h1>
            <div className="flex items-center gap-3 mt-1 flex-wrap">
              <span
                className="mono text-xs px-2 py-0.5 rounded"
                style={{ background: 'rgba(255,255,255,0.07)', color: 'rgba(255,255,255,0.4)' }}
              >
                S{String(show.current_season).padStart(2, '0')}
              </span>
              <span className="mono text-xs" style={{ color: 'var(--emerald)' }}>
                E{show.current_window_start}–E{show.current_window_start + show.buffer_size - 1} buffered
              </span>
              <span
                className="text-xs font-semibold px-2 py-0.5 rounded-full"
                style={{
                  background: show.status === 'Active' ? 'rgba(16,185,129,0.12)' : 'rgba(245,158,11,0.12)',
                  color: show.status === 'Active' ? '#6ee7b7' : '#fcd34d',
                  border: `1px solid ${show.status === 'Active' ? 'rgba(16,185,129,0.3)' : 'rgba(245,158,11,0.3)'}`,
                }}
              >
                {show.status}
              </span>
            </div>
          </div>
          <button
            onClick={handleRefresh}
            disabled={refreshing}
            className="flex items-center gap-2 px-3 py-2 rounded-lg text-sm font-medium transition-all"
            style={{
              background: 'rgba(16,185,129,0.1)',
              border: '1px solid rgba(16,185,129,0.25)',
              color: '#6ee7b7',
            }}
          >
            <RefreshCw size={13} className={refreshing ? 'animate-spin' : ''} />
            Refresh
          </button>
        </div>

        {/* Trackers */}
        <div
          className="rounded-xl p-5 space-y-4"
          style={{ background: 'var(--bg-card)', border: '1px solid var(--border)' }}
        >
          <h2 className="text-sm font-bold tracking-wider uppercase" style={{ color: 'rgba(255,255,255,0.5)' }}>
            Trackers
          </h2>
          {activeTrackers.length === 0 && (
            <p className="text-sm" style={{ color: 'rgba(255,255,255,0.3)' }}>No active trackers.</p>
          )}
          <div className="space-y-2">
            {activeTrackers.map((tracker) => (
              <TrackerRow
                key={tracker.id}
                tracker={tracker}
                onDrop={() => setDropConfirm({ showId: show.id, userId: tracker.user_id, username: tracker.plex_username })}
              />
            ))}
          </div>
          {inactiveTrackers.length > 0 && (
            <div className="pt-2 border-t" style={{ borderColor: 'rgba(255,255,255,0.06)' }}>
              <p className="text-[11px] font-semibold uppercase tracking-wider mb-2" style={{ color: 'rgba(255,255,255,0.25)' }}>
                Inactive
              </p>
              <div className="flex flex-wrap gap-2">
                {inactiveTrackers.map((t) => (
                  <TrackerBadge key={t.id} tracker={t} />
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Episodes by season */}
        {Object.entries(episodesBySeason)
          .sort(([a], [b]) => Number(a) - Number(b))
          .map(([season, episodes]) => (
            <div
              key={season}
              className="rounded-xl overflow-hidden"
              style={{ background: 'var(--bg-card)', border: '1px solid var(--border)' }}
            >
              <div
                className="px-5 py-3 flex items-center justify-between"
                style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}
              >
                <h3 className="text-sm font-bold">
                  Season {season}
                </h3>
                <div className="w-48">
                  <WindowProgress
                    totalEpisodes={episodes.length}
                    windowStart={Number(season) === show.current_season ? show.current_window_start : 1}
                    bufferSize={show.buffer_size}
                    season={Number(season)}
                  />
                </div>
              </div>
              <table className="w-full text-sm">
                <thead>
                  <tr style={{ borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
                    {['Ep', 'Status', ...activeTrackers.map((t) => t.plex_username)].map((h) => (
                      <th
                        key={h}
                        className="px-4 py-2.5 text-left text-[11px] font-semibold tracking-wider uppercase"
                        style={{ color: 'rgba(255,255,255,0.3)' }}
                      >
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {episodes
                    .sort((a, b) => a.episode_number - b.episode_number)
                    .map((ep) => {
                      const st = EP_STATUS_STYLES[ep.status];
                      return (
                        <tr
                          key={ep.id}
                          style={{ borderBottom: '1px solid rgba(255,255,255,0.03)' }}
                        >
                          <td className="px-4 py-2.5 mono text-xs" style={{ color: 'rgba(255,255,255,0.5)' }}>
                            E{String(ep.episode_number).padStart(2, '0')}
                          </td>
                          <td className="px-4 py-2.5">
                            <span className="inline-flex items-center gap-1.5 text-xs" style={{ color: st.color }}>
                              {st.icon}
                              {st.label}
                            </span>
                          </td>
                          {activeTrackers.map((tracker) => {
                            const watched = tracker.last_watched_season > Number(season)
                              || (tracker.last_watched_season === Number(season) && tracker.last_watched_episode >= ep.episode_number);
                            return (
                              <td key={tracker.id} className="px-4 py-2.5 text-center">
                                {watched
                                  ? <Check size={12} style={{ color: 'var(--emerald)', margin: '0 auto' }} />
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

function TrackerRow({ tracker, onDrop }: { tracker: TrackerWithUser; onDrop: () => void }) {
  return (
    <div
      className="flex items-center justify-between p-3 rounded-lg"
      style={{ background: 'rgba(255,255,255,0.03)' }}
    >
      <TrackerBadge tracker={tracker} />
      <button
        onClick={onDrop}
        className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg text-xs font-medium transition-all"
        style={{ color: 'rgba(255,255,255,0.3)', background: 'transparent' }}
        onMouseEnter={(e) => {
          (e.currentTarget as HTMLButtonElement).style.background = 'rgba(239,68,68,0.12)';
          (e.currentTarget as HTMLButtonElement).style.color = '#fca5a5';
        }}
        onMouseLeave={(e) => {
          (e.currentTarget as HTMLButtonElement).style.background = 'transparent';
          (e.currentTarget as HTMLButtonElement).style.color = 'rgba(255,255,255,0.3)';
        }}
      >
        <UserX size={12} />
        Drop
      </button>
    </div>
  );
}
