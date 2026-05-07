import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { RefreshCw, Users, Clock } from 'lucide-react';
import { api } from '../api/client';
import type { ShowSummary } from '../api/client';

const STATUS_BADGE: Record<string, { bg: string; border: string; color: string }> = {
  active:   { bg: 'rgba(34,197,94,0.15)',  border: 'rgba(34,197,94,0.25)',  color: '#22c55e' },
  inactive: { bg: 'rgba(245,158,11,0.15)', border: 'rgba(245,158,11,0.25)', color: '#f59e0b' },
  removed:  { bg: 'rgba(239,68,68,0.15)',  border: 'rgba(239,68,68,0.25)',  color: '#ef4444' },
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

function relativeTime(iso: string | null): string {
  if (!iso) return 'Never';
  const diff = Date.now() - new Date(iso).getTime();
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return 'Just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

interface ShowCardProps {
  show: ShowSummary;
  delay?: number;
  onReconcile?: () => void;
}

export function ShowCard({ show, delay = 0, onReconcile }: ShowCardProps) {
  const navigate = useNavigate();
  const [reconciling, setReconciling] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

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
        {/* Poster placeholder */}
        <div className="show-poster">
          <div
            className="show-poster-placeholder"
            style={{ background: posterGradient(show.title) }}
          >
            {posterInitials(show.title)}
          </div>
        </div>

        {/* Right column */}
        <div style={{
          flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column',
          justifyContent: 'space-between', padding: '16px 16px 16px 14px',
        }}>
          {/* Title + status */}
          <div style={{ marginBottom: 10 }}>
            <div style={{
              fontWeight: 700, fontSize: '0.9rem', color: 'var(--color-text-primary)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              lineHeight: 1.25, marginBottom: 5,
            }}>
              {show.title}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 5, flexWrap: 'wrap' }}>
              <span style={{
                fontSize: '0.68rem', fontWeight: 700, padding: '2px 8px',
                borderRadius: 'var(--radius-pill)',
                background: s.bg, border: `1px solid ${s.border}`, color: s.color,
              }}>
                {show.status}
              </span>
              <span style={{
                fontSize: '0.68rem', fontWeight: 600,
                background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
                padding: '2px 7px', borderRadius: 4, color: 'var(--color-text-muted)',
                fontVariantNumeric: 'tabular-nums',
              }}>
                buffer {show.effective_buffer_size}
              </span>
            </div>
          </div>

          {/* Meta row */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
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

          {/* Footer */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
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
                  (e.currentTarget as HTMLButtonElement).style.background = 'rgba(249,115,22,0.15)';
                  (e.currentTarget as HTMLButtonElement).style.color = 'var(--color-accent-orange)';
                  (e.currentTarget as HTMLButtonElement).style.borderColor = 'rgba(249,115,22,0.3)';
                }
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLButtonElement).style.background = 'var(--color-glass-bg-light)';
                (e.currentTarget as HTMLButtonElement).style.color = 'var(--color-text-muted)';
                (e.currentTarget as HTMLButtonElement).style.borderColor = 'var(--color-glass-border)';
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
