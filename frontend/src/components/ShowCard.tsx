import { useState, useRef, useLayoutEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { RefreshCw, Users, Clock } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary, UserBufferInfo } from '../api/client';

// ── User color system ──────────────────────────────────────────────────────────
export const USER_COLORS = [
  '#3b82f6', '#ef4444', '#22c55e', '#a855f7',
  '#06b6d4', '#eab308', '#ec4899', '#f97316',
  '#84cc16', '#14b8a6',
];

export function buildUserColorMap(shows: ShowSummary[]): Record<string, string> {
  const map: Record<string, string> = {};
  let idx = 0;
  for (const show of shows) {
    for (const buf of show.user_buffers ?? []) {
      if (!(buf.display_name in map)) {
        map[buf.display_name] = USER_COLORS[idx % USER_COLORS.length];
        idx++;
      }
    }
  }
  return map;
}

const OVERFLOW_GREY = '#9ca3af';

function stripeBg(colors: string[]): string {
  if (colors.length === 1) return colors[0];
  const slices = colors.length <= 2 ? colors : [colors[0], colors[1], OVERFLOW_GREY];
  const stops: string[] = [];
  const step = 100 / slices.length;
  slices.forEach((c, i) => {
    stops.push(`${c} ${i * step}%`);
    stops.push(`${c} ${(i + 1) * step}%`);
  });
  return `linear-gradient(135deg, ${stops.join(', ')})`;
}

// ── WindowProgress ────────────────────────────────────────────────────────────
interface WindowProgressProps {
  seasonBuffers: UserBufferInfo[];   // buffers for the currently viewed season
  allBuffers: UserBufferInfo[];      // all buffers across all seasons (for legend)
  viewingSeason: number;
  colorMap: Record<string, string>;
  totalEpisodes?: number;            // use exact count if available; else estimates from buffers
}

export function WindowProgress({
  seasonBuffers,
  allBuffers,
  viewingSeason,
  colorMap,
  totalEpisodes: providedTotal,
}: WindowProgressProps) {
  const stripRef = useRef<HTMLDivElement>(null);
  const [perRow, setPerRow] = useState<number>(100);

  const maxBufEnd = seasonBuffers.reduce((m, b) => Math.max(m, b.buffer_end), 0);
  const totalEpisodes = providedTotal ?? Math.max(maxBufEnd + 3, 1);

  useLayoutEffect(() => {
    function measure() {
      const el = stripRef.current;
      if (!el) return;
      const w = el.clientWidth;
      if (!w) return;
      const MIN_CELL = 28;
      const GAP = 3;
      const cap = Math.max(1, Math.floor((w + GAP) / (MIN_CELL + GAP)));
      const fits = Math.min(totalEpisodes, cap);
      const rows = Math.max(1, Math.ceil(totalEpisodes / fits));
      setPerRow(Math.ceil(totalEpisodes / rows));
    }
    measure();
    const ro = new ResizeObserver(measure);
    if (stripRef.current) ro.observe(stripRef.current);
    return () => ro.disconnect();
  }, [totalEpisodes]);

  const totalTrackers = seasonBuffers.length;

  const cells = Array.from({ length: totalEpisodes }, (_, i) => {
    const ep = i + 1;
    // A user has "watched" ep if ep is before their buffer_start
    const watchedBy = seasonBuffers.filter(b => ep < b.buffer_start);
    const bufferingBy = seasonBuffers.filter(b => ep >= b.buffer_start && ep <= b.buffer_end);
    return { ep, watchedBy, bufferingBy };
  });

  return (
    <div>
      <div
        ref={stripRef}
        className="ep-strip"
        style={{ gridTemplateColumns: `repeat(${perRow}, minmax(0, 1fr))` }}
      >
        {cells.map(({ ep, watchedBy, bufferingBy }) => {
          if (bufferingBy.length > 0) {
            const colors = bufferingBy.map(b => colorMap[b.display_name] ?? '#f97316');
            const bg = stripeBg(colors);
            const names = bufferingBy.map(b => b.display_name).join(' + ');
            return (
              <div
                key={ep}
                className="ep-cell"
                style={{
                  background: bg,
                  boxShadow: bufferingBy.length === 1
                    ? `0 0 6px ${colors[0]}55, inset 0 0 0 1px ${colors[0]}80`
                    : '0 0 6px rgba(255,255,255,0.15), inset 0 0 0 1px rgba(255,255,255,0.18)',
                }}
                title={`E${String(ep).padStart(2, '0')} · Buffered for ${names}`}
              >
                <span className="ep-cell-num">E{ep}</span>
              </div>
            );
          }
          const allWatched = totalTrackers > 0 && watchedBy.length === totalTrackers;
          if (watchedBy.length > 0) {
            return (
              <div
                key={ep}
                className="ep-cell ep-watched"
                style={{ opacity: allWatched ? 0.55 : 0.8 }}
                title={`E${String(ep).padStart(2, '0')} · Watched`}
              >
                <span className="ep-cell-num muted">E{ep}</span>
              </div>
            );
          }
          if (viewingSeason === 1 && ep === 1) {
            return (
              <div key={ep} className="ep-cell ep-starter" title="E01 · Starter buffer (always kept)">
                <span className="ep-cell-num">E1</span>
              </div>
            );
          }
          return (
            <div key={ep} className="ep-cell ep-upcoming" title={`E${String(ep).padStart(2, '0')} · Upcoming`}>
              <span className="ep-cell-num muted">E{ep}</span>
            </div>
          );
        })}
      </div>

      {/* User legend pills */}
      {allBuffers.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginTop: 9 }}>
          {allBuffers.map((buf) => {
            const color = colorMap[buf.display_name] ?? '#f97316';
            const onSeason = buf.season === viewingSeason;
            const lastWatched = buf.buffer_start - 1;
            const label = onSeason
              ? (lastWatched > 0 ? `E${String(lastWatched).padStart(2, '0')}` : 'E00')
              : `S${String(buf.season).padStart(2, '0')}E${String(lastWatched).padStart(2, '0')}`;
            return (
              <span
                key={`${buf.display_name}-${buf.season}`}
                title={`${buf.display_name} · S${String(buf.season).padStart(2, '0')}E${String(lastWatched).padStart(2, '0')}`}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: 5,
                  padding: '2px 8px 2px 3px', borderRadius: 'var(--radius-pill)',
                  background: `${color}15`, border: `1px solid ${color}40`,
                  fontSize: '0.62rem', fontWeight: 700, color,
                  fontVariantNumeric: 'tabular-nums', cursor: 'default',
                  opacity: onSeason ? 1 : 0.55,
                }}
              >
                <span style={{
                  width: 14, height: 14, borderRadius: '50%', background: color,
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: '0.5rem', fontWeight: 800, color: '#fff', flexShrink: 0,
                }}>
                  {buf.display_name.charAt(0).toUpperCase()}
                </span>
                {label}
              </span>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ── Show Poster ───────────────────────────────────────────────────────────────
const POSTER_GRADIENTS = [
  ['#1a1a2e', '#16213e', '#0f3460'],
  ['#2d1b33', '#3d1f4a', '#1a0a2e'],
  ['#0d1b2a', '#1b2838', '#243447'],
  ['#1a2a1a', '#1f3a2a', '#0d2b1a'],
  ['#2a1a0d', '#3a2515', '#1f1005'],
  ['#1a0d2a', '#2a1a3a', '#150825'],
  ['#0d2a2a', '#1a3a3a', '#082020'],
  ['#2a1a1a', '#3a2525', '#200d0d'],
];

function posterGradient(title: string): string {
  const idx = title.split('').reduce((a, c) => a + c.charCodeAt(0), 0) % POSTER_GRADIENTS.length;
  const [c1, c2, c3] = POSTER_GRADIENTS[idx];
  return `linear-gradient(160deg, ${c1} 0%, ${c2} 55%, ${c3} 100%)`;
}

function posterInitials(title: string): string {
  return title.split(' ').filter(Boolean).slice(0, 2).map((w) => w[0].toUpperCase()).join('');
}

function ShowPoster({ title, posterUrl }: { title: string; posterUrl?: string }) {
  const [loaded, setLoaded] = useState(false);
  const [errored, setErrored] = useState(false);
  return (
    <div className="show-poster">
      {/* Gradient fallback always rendered beneath */}
      <div
        className="show-poster-placeholder"
        style={{ background: posterGradient(title), position: 'absolute', inset: 0, minHeight: 160 }}
      >
        {(!loaded || errored) && posterInitials(title)}
      </div>
      {posterUrl && !errored && (
        <img
          src={posterUrl}
          alt={title}
          style={{
            width: '100%', height: '100%', objectFit: 'cover', display: 'block',
            opacity: loaded ? 1 : 0, transition: 'opacity 0.4s ease',
            position: 'relative', zIndex: 1,
          }}
          onLoad={() => setLoaded(true)}
          onError={() => setErrored(true)}
        />
      )}
    </div>
  );
}

// ── Show Card ─────────────────────────────────────────────────────────────────
const STATUS_BADGE: Record<string, { bg: string; border: string; color: string }> = {
  active:   { bg: 'rgba(34,197,94,0.15)',  border: 'rgba(34,197,94,0.25)',  color: '#22c55e' },
  inactive: { bg: 'rgba(245,158,11,0.15)', border: 'rgba(245,158,11,0.25)', color: '#f59e0b' },
  removed:  { bg: 'rgba(239,68,68,0.15)',  border: 'rgba(239,68,68,0.25)',  color: '#ef4444' },
};

function relativeTime(iso: string | null): string {
  if (!iso) return 'Never';
  const diff = Date.now() - new Date(iso).getTime();
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return 'Just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

interface ShowCardProps {
  show: ShowSummary;
  delay?: number;
  colorMap?: Record<string, string>;
  onReconcile?: () => void;
  onHover?: (show: ShowSummary) => void;
  onHoverEnd?: () => void;
}

export function ShowCard({
  show,
  delay = 0,
  colorMap = {},
  onReconcile,
  onHover,
  onHoverEnd,
}: ShowCardProps) {
  const navigate = useNavigate();
  const [reconciling, setReconciling] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  const userBuffers = show.user_buffers ?? [];

  // Unique seasons, in order
  const seasons = Array.from(new Set(userBuffers.map((b) => b.season))).sort((a, b) => a - b);
  const [activeSeason, setActiveSeason] = useState<number | null>(null);
  const displaySeason = activeSeason ?? seasons[0] ?? 1;

  const seasonBuffers = userBuffers.filter((b) => b.season === displaySeason);
  const s = STATUS_BADGE[show.status] ?? STATUS_BADGE.inactive;

  async function handleReconcile(e: React.MouseEvent) {
    e.stopPropagation();
    setReconciling(true);
    try {
      await api.reconcileShow(show.tvdb_id);
      setToast('Reconcile queued');
      onReconcile?.();
    } catch {
      setToast('Reconcile failed');
    }
    setTimeout(() => { setReconciling(false); setToast(null); }, 2000);
  }

  return (
    <div
      className="show-card"
      style={{ animationDelay: `${delay}ms`, position: 'relative' }}
      onClick={() => navigate(`/shows/${show.tvdb_id}`)}
      onMouseEnter={() => onHover?.(show)}
      onMouseLeave={() => onHoverEnd?.()}
    >
      {show.status === 'active' && (
        <div className="active-dot" style={{ position: 'absolute', top: 12, right: 12 }} />
      )}

      {toast && (
        <div style={{
          position: 'absolute', top: 8, left: '50%', transform: 'translateX(-50%)',
          background: 'rgba(30,30,40,0.95)', border: '1px solid var(--color-glass-border)',
          borderRadius: 'var(--radius-pill)', padding: '4px 12px',
          fontSize: '0.72rem', fontWeight: 600, color: 'var(--color-text-secondary)',
          zIndex: 10, whiteSpace: 'nowrap',
        }}>
          {toast}
        </div>
      )}

      <div style={{ display: 'flex', gap: 0, flex: 1, alignItems: 'stretch' }}>
        {/* Poster */}
        <ShowPoster title={show.title} posterUrl={show.poster_url} />

        {/* Right column */}
        <div style={{
          flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column',
          justifyContent: 'space-between', padding: '16px 16px 16px 14px',
        }}>
          {/* Title + season pills + status */}
          <div style={{ marginBottom: 10 }}>
            <div style={{
              fontWeight: 700, fontSize: '0.9rem', color: 'var(--color-text-primary)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              lineHeight: 1.25, marginBottom: 6,
            }}>
              {show.title}
            </div>

            <div
              style={{ display: 'flex', alignItems: 'center', gap: 4, flexWrap: 'wrap' }}
              onClick={(e) => e.stopPropagation()}
            >
              {/* Season switcher pills */}
              {seasons.map((sn) => {
                const isActive = sn === displaySeason;
                const trackersOnSeason = userBuffers.filter((b) => b.season === sn);
                return (
                  <button
                    key={sn}
                    onClick={(e) => { e.stopPropagation(); setActiveSeason(sn); }}
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
                    {trackersOnSeason.length > 0 && (
                      <span style={{
                        minWidth: 14, height: 14, padding: '0 3px',
                        borderRadius: 'var(--radius-pill)',
                        fontSize: '0.5rem', fontWeight: 800,
                        background: isActive ? 'var(--color-accent-orange)' : 'rgba(255,255,255,0.15)',
                        color: '#fff',
                        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                      }}>
                        {trackersOnSeason.length}
                      </span>
                    )}
                  </button>
                );
              })}

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
          {seasons.length > 0 && (
            <div style={{ marginBottom: 10 }}>
              <WindowProgress
                seasonBuffers={seasonBuffers}
                allBuffers={userBuffers}
                viewingSeason={displaySeason}
                colorMap={colorMap}
              />
            </div>
          )}

          {/* Footer */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 6 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: '0.72rem', color: 'var(--color-text-muted)' }}>
                <Users size={11} />
                <span className="mono">{show.active_request_count} watching</span>
              </span>
              {show.last_activity_at && (
                <span style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: '0.72rem', color: 'var(--color-text-muted)' }}>
                  <Clock size={11} />
                  <span>{relativeTime(show.last_activity_at)}</span>
                </span>
              )}
            </div>

            <button
              onClick={handleReconcile}
              disabled={reconciling}
              style={{
                display: 'inline-flex', alignItems: 'center', gap: 4,
                padding: '4px 10px', borderRadius: 'var(--radius-pill)',
                border: '1px solid var(--color-glass-border)',
                background: 'var(--color-glass-bg-light)',
                cursor: reconciling ? 'not-allowed' : 'pointer',
                color: 'var(--color-text-muted)', fontSize: '0.72rem', fontWeight: 600,
                transition: 'all 0.15s', opacity: reconciling ? 0.6 : 1,
              }}
              onMouseEnter={(e) => {
                if (!reconciling) {
                  const b = e.currentTarget as HTMLButtonElement;
                  b.style.background = 'rgba(249,115,22,0.15)';
                  b.style.color = 'var(--color-accent-orange)';
                  b.style.borderColor = 'rgba(249,115,22,0.3)';
                }
              }}
              onMouseLeave={(e) => {
                const b = e.currentTarget as HTMLButtonElement;
                b.style.background = 'var(--color-glass-bg-light)';
                b.style.color = 'var(--color-text-muted)';
                b.style.borderColor = 'var(--color-glass-border)';
              }}
            >
              <RefreshCw size={11} style={{ animation: reconciling ? 'spin 0.8s linear infinite' : 'none' }} />
              Reconcile
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
