import type { TrackerWithUser } from '../api/client';

const WRAP_THRESHOLD = 16;
const USER_COLORS = ['#3b82f6', '#a855f7', '#ec4899', '#06b6d4', '#84cc16', '#f43f5e'];

export function userColor(trackerId: number): string {
  return USER_COLORS[trackerId % USER_COLORS.length];
}

interface WindowProgressProps {
  totalEpisodes: number;
  windowStart: number;
  bufferSize: number;
  trackers?: TrackerWithUser[];
  isCurrent?: boolean;
}

export function WindowProgress({
  totalEpisodes,
  windowStart,
  bufferSize,
  trackers = [],
  isCurrent = true,
}: WindowProgressProps) {
  if (!totalEpisodes) return null;

  const windowEnd = isCurrent ? Math.min(windowStart + bufferSize - 1, totalEpisodes) : 0;
  const wrap = totalEpisodes > WRAP_THRESHOLD;
  const perRow = wrap ? Math.ceil(totalEpisodes / 2) : totalEpisodes;

  const cells = Array.from({ length: totalEpisodes }, (_, i) => {
    const ep = i + 1;
    const isWatched  = isCurrent ? ep < windowStart : false;
    const isBuffered = isCurrent ? ep >= windowStart && ep <= windowEnd : false;
    return { ep, isWatched, isBuffered };
  });

  const rows = wrap ? [cells.slice(0, perRow), cells.slice(perRow)] : [cells];

  const markers = trackers.map((t) => {
    const ep = t.last_watched_episode;
    if (!ep || ep < 1 || ep > totalEpisodes) return null;
    const row  = wrap && ep > perRow ? 1 : 0;
    const cell = wrap && ep > perRow ? ep - perRow - 1 : ep - 1;
    return { tracker: t, ep, row, cell };
  }).filter(Boolean) as { tracker: TrackerWithUser; ep: number; row: number; cell: number }[];

  return (
    <div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {rows.map((row, ri) => {
          const rowMarkers = markers.filter((m) => m.row === ri);
          return (
            <div key={ri}>
              {rowMarkers.length > 0 && (
                <div style={{ position: 'relative', height: 14, marginBottom: 2 }}>
                  {rowMarkers.map((m) => {
                    const pct = ((m.cell + 0.5) / row.length) * 100;
                    const color = userColor(m.tracker.id);
                    const label = `S${String(m.tracker.last_watched_season).padStart(2,'0')}E${String(m.ep).padStart(2,'0')}`;
                    return (
                      <div
                        key={m.tracker.id}
                        title={`${m.tracker.plex_username} · ${label}`}
                        style={{
                          position: 'absolute', left: `${pct}%`,
                          transform: 'translateX(-50%)',
                          display: 'flex', flexDirection: 'column',
                          alignItems: 'center', gap: 1, cursor: 'default',
                        }}
                      >
                        <div style={{
                          width: 14, height: 14, borderRadius: '50%',
                          background: color,
                          display: 'flex', alignItems: 'center', justifyContent: 'center',
                          fontSize: '0.5rem', fontWeight: 800, color: '#fff',
                          boxShadow: `0 0 6px ${color}88`, flexShrink: 0,
                        }}>
                          {m.tracker.plex_username.charAt(0).toUpperCase()}
                        </div>
                        <div style={{ width: 1, height: 3, background: color, opacity: 0.7 }} />
                      </div>
                    );
                  })}
                </div>
              )}
              <div style={{ display: 'flex', gap: 2 }}>
                {row.map(({ ep, isWatched, isBuffered }) => (
                  <div
                    key={ep}
                    className={`ep-cell ${isWatched ? 'ep-watched' : isBuffered ? 'ep-buffered' : 'ep-upcoming'}`}
                    style={{ flex: 1, height: 8 }}
                    title={`E${String(ep).padStart(2,'0')} · ${isWatched ? 'Watched' : isBuffered ? 'Buffered' : 'Upcoming'}`}
                  />
                ))}
              </div>
            </div>
          );
        })}
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 5 }}>
        <span style={{ fontSize: '0.65rem', color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>E1</span>
        {isCurrent && (
          <span style={{ fontSize: '0.65rem', color: 'var(--color-accent-orange)', fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>
            E{windowStart}–E{windowEnd} buffered
          </span>
        )}
        <span style={{ fontSize: '0.65rem', color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>E{totalEpisodes}</span>
      </div>

      {trackers.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginTop: 7 }}>
          {trackers.map((t) => {
            const color = userColor(t.id);
            const ep = String(t.last_watched_episode).padStart(2, '0');
            const label = `S${String(t.last_watched_season).padStart(2,'0')}E${ep}`;
            return (
              <span
                key={t.id}
                title={`${t.plex_username} · ${label}`}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: 4,
                  padding: '2px 6px 2px 3px',
                  borderRadius: 'var(--radius-pill)',
                  background: `${color}15`,
                  border: `1px solid ${color}35`,
                  fontSize: '0.62rem', fontWeight: 700,
                  color, fontVariantNumeric: 'tabular-nums', cursor: 'default',
                }}
              >
                <span style={{
                  width: 12, height: 12, borderRadius: '50%', background: color,
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: '0.45rem', fontWeight: 800, color: '#fff', flexShrink: 0,
                }}>
                  {t.plex_username.charAt(0).toUpperCase()}
                </span>
                E{ep}
              </span>
            );
          })}
        </div>
      )}
    </div>
  );
}
