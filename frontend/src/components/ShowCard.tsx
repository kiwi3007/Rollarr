import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { RefreshCw, Users, Tv2 } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary } from '../api/client';
import { WindowProgress } from './WindowProgress';

const STATUS_STYLES = {
  Active:    { bg: 'rgba(16,185,129,0.12)',  border: 'rgba(16,185,129,0.3)',  color: '#6ee7b7',  dot: '#10b981' },
  Stale:     { bg: 'rgba(245,158,11,0.12)',  border: 'rgba(245,158,11,0.3)',  color: '#fcd34d',  dot: '#f59e0b' },
  Completed: { bg: 'rgba(59,130,246,0.12)',  border: 'rgba(59,130,246,0.3)',  color: '#93c5fd',  dot: '#3b82f6' },
};

interface ShowCardProps {
  show: ShowSummary;
  style?: React.CSSProperties;
}

export function ShowCard({ show, style }: ShowCardProps) {
  const navigate = useNavigate();
  const [refreshing, setRefreshing] = useState(false);

  const s = STATUS_STYLES[show.status] ?? STATUS_STYLES.Stale;
  const windowEnd = Math.min(show.current_window_start + show.buffer_size - 1, 99);

  async function handleRefresh(e: React.MouseEvent) {
    e.stopPropagation();
    setRefreshing(true);
    await api.refreshShow(show.id);
    setTimeout(() => setRefreshing(false), 1500);
  }

  return (
    <div
      onClick={() => navigate(`/shows/${show.id}`)}
      className="group relative rounded-xl p-5 cursor-pointer transition-all duration-200 fade-up"
      style={{
        background: 'var(--bg-card)',
        border: '1px solid var(--border)',
        ...style,
      }}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(16,185,129,0.2)';
        (e.currentTarget as HTMLDivElement).style.transform = 'translateY(-2px)';
        (e.currentTarget as HTMLDivElement).style.boxShadow = '0 12px 40px rgba(0,0,0,0.4)';
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--border)';
        (e.currentTarget as HTMLDivElement).style.transform = '';
        (e.currentTarget as HTMLDivElement).style.boxShadow = '';
      }}
    >
      {/* Status dot */}
      {show.status === 'Active' && (
        <span
          className="absolute top-4 right-4 w-2 h-2 rounded-full active-glow"
          style={{ background: s.dot }}
        />
      )}

      {/* Header */}
      <div className="flex items-start gap-3 mb-4">
        <div
          className="p-2 rounded-lg shrink-0"
          style={{ background: 'rgba(255,255,255,0.05)' }}
        >
          <Tv2 size={16} style={{ color: 'rgba(255,255,255,0.5)' }} />
        </div>
        <div className="min-w-0">
          <h3 className="font-bold text-[15px] leading-tight truncate text-white group-hover:text-emerald-300 transition-colors">
            {show.title}
          </h3>
          <div className="flex items-center gap-2 mt-1">
            <span
              className="mono text-[10px] font-semibold px-1.5 py-0.5 rounded"
              style={{ background: 'rgba(255,255,255,0.07)', color: 'rgba(255,255,255,0.5)' }}
            >
              S{String(show.current_season).padStart(2, '0')}
            </span>
            <span className="mono text-[11px]" style={{ color: 'var(--emerald)' }}>
              E{show.current_window_start}–E{windowEnd} buffered
            </span>
          </div>
        </div>
      </div>

      {/* Window progress */}
      <div className="mb-4">
        <WindowProgress
          totalEpisodes={20}
          windowStart={show.current_window_start}
          bufferSize={show.buffer_size}
          season={show.current_season}
        />
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          {/* Status pill */}
          <span
            className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-semibold"
            style={{ background: s.bg, border: `1px solid ${s.border}`, color: s.color }}
          >
            {show.status}
          </span>
          {/* Tracker count */}
          <span className="flex items-center gap-1 text-xs" style={{ color: 'rgba(255,255,255,0.4)' }}>
            <Users size={11} />
            <span className="mono">{show.trackerCount}</span>
          </span>
        </div>

        {/* Refresh button */}
        <button
          onClick={handleRefresh}
          disabled={refreshing}
          className="p-1.5 rounded-lg transition-all"
          style={{
            background: 'rgba(255,255,255,0.05)',
            color: 'rgba(255,255,255,0.4)',
          }}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLButtonElement).style.background = 'rgba(16,185,129,0.15)';
            (e.currentTarget as HTMLButtonElement).style.color = '#6ee7b7';
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLButtonElement).style.background = 'rgba(255,255,255,0.05)';
            (e.currentTarget as HTMLButtonElement).style.color = 'rgba(255,255,255,0.4)';
          }}
          title="Force refresh"
        >
          <RefreshCw size={13} className={refreshing ? 'animate-spin' : ''} />
        </button>
      </div>
    </div>
  );
}
