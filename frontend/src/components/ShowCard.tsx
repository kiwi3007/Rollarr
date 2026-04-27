import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { RefreshCw, Users } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary } from '../api/client';
import { WindowProgress } from './WindowProgress';

const STATUS_BADGE: Record<string, { bg: string; border: string; color: string }> = {
  Active:    { bg: 'rgba(34,197,94,0.15)',  border: 'rgba(34,197,94,0.25)',  color: '#22c55e' },
  Stale:     { bg: 'rgba(245,158,11,0.15)', border: 'rgba(245,158,11,0.25)', color: '#f59e0b' },
  Completed: { bg: 'rgba(59,130,246,0.15)', border: 'rgba(59,130,246,0.25)', color: '#3b82f6' },
};

const POSTER_GRADIENTS = [
  ['#1a1a2e','#16213e','#0f3460'],
  ['#2d1b33','#3d1f4a','#1a0a2e'],
  ['#0d1b2a','#1b2838','#243447'],
  ['#1a2a1a','#1f3a2a','#0d2b1a'],
  ['#2a1a0d','#3a2515','#1f1005'],
  ['#1a0d2a','#2a1a3a','#150825'],
  ['#0d2a2a','#1a3a3a','#082020'],
  ['#2a1a1a','#3a2525','#200d0d'],
];

function posterGradient(title: string): string {
  const idx = title.split('').reduce((a, c) => a + c.charCodeAt(0), 0) % POSTER_GRADIENTS.length;
  const [c1, c2, c3] = POSTER_GRADIENTS[idx];
  return `linear-gradient(160deg, ${c1} 0%, ${c2} 55%, ${c3} 100%)`;
}

function posterInitials(title: string): string {
  return title.split(' ').filter(Boolean).slice(0, 2).map((w) => w[0].toUpperCase()).join('');
}

interface ShowCardProps {
  show: ShowSummary;
  delay?: number;
}

function defaultSeason(show: ShowSummary): number {
  const activeTrackers = show.trackers.filter((t) => t.is_active === 1);
  if (activeTrackers.length > 0) {
    return Math.min(...activeTrackers.map((t) => t.last_watched_season));
  }
  return show.current_season;
}

export function ShowCard({ show, delay = 0 }: ShowCardProps) {
  const navigate = useNavigate();
  const [refreshing, setRefreshing] = useState(false);
  const [activeSeason, setActiveSeason] = useState(() => defaultSeason(show));

  const s = STATUS_BADGE[show.status] ?? STATUS_BADGE.Stale;
  const activeTrackers = show.trackers.filter((t) => t.is_active === 1);

  const seasons = [...new Set([
    ...(show.season_data ?? []).map((sd) => sd.season),
    show.current_season,
  ])].sort((a, b) => a - b);

  const multiSeason = seasons.length > 1;
  const seasonTrackers = activeTrackers.filter((t) => t.last_watched_season === activeSeason);
  const isCurrent = activeSeason === show.current_season && seasonTrackers.length > 0;

  const seasonInfo = (show.season_data ?? []).find((sd) => sd.season === activeSeason)
    ?? { season: activeSeason, total_episodes: 0 };

  async function handleRefresh(e: React.MouseEvent) {
    e.stopPropagation();
    setRefreshing(true);
    await api.refreshShow(show.id);
    setTimeout(() => setRefreshing(false), 1500);
  }

  function handleSeasonClick(e: React.MouseEvent, sn: number) {
    e.stopPropagation();
    setActiveSeason(sn);
  }

  return (
    <div
      className="show-card"
      style={{ animationDelay: `${delay}ms` }}
      onClick={() => navigate(`/shows/${show.id}`)}
    >
      {show.status === 'Active' && (
        <div className="active-dot" style={{ position: 'absolute', top: 12, right: 12 }} />
      )}

      <div style={{ display: 'flex', gap: 0, flex: 1, alignItems: 'stretch' }}>
        {/* Poster */}
        <div className="show-poster">
          {show.poster_url ? (
            <img
              src={show.poster_url}
              alt={show.title}
              style={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block', minHeight: 160 }}
            />
          ) : (
            <div
              className="show-poster-placeholder"
              style={{ background: posterGradient(show.title) }}
            >
              {posterInitials(show.title)}
            </div>
          )}
        </div>

        {/* Right column */}
        <div style={{
          flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column',
          justifyContent: 'space-between', padding: '16px 16px 16px 14px',
        }}>
          {/* Title + season/status */}
          <div style={{ marginBottom: 10 }}>
            <div style={{
              fontWeight: 700, fontSize: '0.9rem', color: 'var(--color-text-primary)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              lineHeight: 1.25, marginBottom: 5,
            }}>
              {show.title}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 5, flexWrap: 'wrap' }}>
              {multiSeason ? (
                seasons.map((sn) => {
                  const isActive = sn === activeSeason;
                  const count = activeTrackers.filter((t) => t.last_watched_season === sn).length;
                  return (
                    <button
                      key={sn}
                      onClick={(e) => handleSeasonClick(e, sn)}
                      style={{
                        display: 'inline-flex', alignItems: 'center', gap: 4,
                        padding: '2px 8px', borderRadius: 'var(--radius-pill)',
                        border: isActive ? '1px solid rgba(249,115,22,0.5)' : '1px solid var(--color-glass-border)',
                        background: isActive ? 'rgba(249,115,22,0.15)' : 'var(--color-glass-bg-light)',
                        color: isActive ? 'var(--color-accent-orange)' : 'var(--color-text-muted)',
                        fontSize: '0.68rem', fontWeight: 700, cursor: 'pointer',
                        transition: 'all 0.15s', fontVariantNumeric: 'tabular-nums',
                      }}
                    >
                      S{String(sn).padStart(2, '0')}
                      {count > 0 && (
                        <span style={{
                          width: 14, height: 14, borderRadius: '50%', fontSize: '0.5rem', fontWeight: 800,
                          background: isActive ? 'var(--color-accent-orange)' : 'rgba(255,255,255,0.15)',
                          color: '#fff', display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                        }}>
                          {count}
                        </span>
                      )}
                    </button>
                  );
                })
              ) : (
                <span style={{
                  background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
                  padding: '2px 7px', borderRadius: 4, fontWeight: 700,
                  fontSize: '0.68rem', color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums',
                }}>
                  S{String(show.current_season).padStart(2, '0')}
                </span>
              )}
              <span style={{
                fontSize: '0.68rem', fontWeight: 700, padding: '2px 8px',
                borderRadius: 'var(--radius-pill)',
                background: s.bg, border: `1px solid ${s.border}`, color: s.color,
              }}>
                {show.status}
              </span>
            </div>
          </div>

          {/* Episode strip */}
          <div style={{ marginBottom: 10 }}>
            <WindowProgress
              totalEpisodes={seasonInfo.total_episodes}
              windowStart={isCurrent ? show.current_window_start : 1}
              bufferSize={show.buffer_size}
              trackers={seasonTrackers}
              isCurrent={isCurrent}
            />
          </div>

          {/* Footer */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: '0.72rem', color: 'var(--color-text-muted)' }}>
              <Users size={11} />
              <span className="mono">{show.trackerCount} watching</span>
            </span>
            <button
              onClick={handleRefresh}
              style={{
                width: 26, height: 26, borderRadius: 'var(--radius-inner)',
                border: '1px solid var(--color-glass-border)',
                background: 'var(--color-glass-bg-light)',
                cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
                color: 'var(--color-text-muted)', transition: 'all 0.15s',
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLButtonElement).style.background = 'rgba(249,115,22,0.15)';
                (e.currentTarget as HTMLButtonElement).style.color = 'var(--color-accent-orange)';
                (e.currentTarget as HTMLButtonElement).style.borderColor = 'rgba(249,115,22,0.3)';
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLButtonElement).style.background = 'var(--color-glass-bg-light)';
                (e.currentTarget as HTMLButtonElement).style.color = 'var(--color-text-muted)';
                (e.currentTarget as HTMLButtonElement).style.borderColor = 'var(--color-glass-border)';
              }}
              title="Force refresh"
            >
              <RefreshCw size={11} style={{ display: 'block', animation: refreshing ? 'spin 0.8s linear infinite' : 'none' }} />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
