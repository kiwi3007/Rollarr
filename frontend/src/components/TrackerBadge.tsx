import type { TrackerWithUser } from '../api/client';

interface TrackerBadgeProps {
  tracker: TrackerWithUser;
}

export function TrackerBadge({ tracker }: TrackerBadgeProps) {
  const lastSeen = tracker.last_watched_episode > 0
    ? `S${String(tracker.last_watched_season).padStart(2,'0')}E${String(tracker.last_watched_episode).padStart(2,'0')}`
    : 'not started';

  return (
    <span
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 6,
        padding: '3px 10px', borderRadius: 'var(--radius-pill)',
        background: tracker.is_active ? 'rgba(249,115,22,0.1)' : 'var(--color-glass-bg-light)',
        border: `1px solid ${tracker.is_active ? 'rgba(249,115,22,0.25)' : 'var(--color-glass-border)'}`,
        fontSize: '0.72rem', fontWeight: 600,
        color: tracker.is_active ? 'var(--color-accent-orange)' : 'var(--color-text-muted)',
      }}
    >
      <span style={{
        width: 18, height: 18, borderRadius: '50%',
        background: tracker.is_active ? 'rgba(249,115,22,0.2)' : 'var(--color-glass-bg-light)',
        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
        fontSize: '0.55rem', fontWeight: 800,
      }}>
        {tracker.plex_username.charAt(0).toUpperCase()}
      </span>
      {tracker.plex_username}
      <span style={{ color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>· {lastSeen}</span>
    </span>
  );
}
