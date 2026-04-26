import { User } from 'lucide-react';
import type { TrackerWithUser } from '../api/client';

interface TrackerBadgeProps {
  tracker: TrackerWithUser;
}

export function TrackerBadge({ tracker }: TrackerBadgeProps) {
  const lastSeen = tracker.last_watched_episode > 0
    ? `S${String(tracker.last_watched_season).padStart(2,'0')}E${String(tracker.last_watched_episode).padStart(2,'0')}`
    : 'not started';

  return (
    <div
      className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs"
      style={{
        background: tracker.is_active ? 'rgba(16,185,129,0.1)' : 'rgba(255,255,255,0.05)',
        border: `1px solid ${tracker.is_active ? 'rgba(16,185,129,0.25)' : 'rgba(255,255,255,0.08)'}`,
        color: tracker.is_active ? '#a7f3d0' : 'rgba(255,255,255,0.4)',
      }}
    >
      <User size={10} />
      <span className="font-medium">{tracker.plex_username}</span>
      <span className="mono opacity-60">·</span>
      <span className="mono opacity-70">{lastSeen}</span>
    </div>
  );
}
