import { useState, useEffect, useCallback } from 'react';
import { Loader2, Flag as FlagIcon, CheckCircle, XCircle } from 'lucide-react';
import { api } from '../api/client';
import type { Flag } from '../api/client';

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

export function FlagsPage() {
  const [flags, setFlags] = useState<Flag[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [updating, setUpdating] = useState<Set<number>>(new Set());

  const load = useCallback(async () => {
    try {
      const data = await api.getFlags();
      setFlags(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load flags');
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleUpdate(id: number, status: 'resolved' | 'ignored') {
    setUpdating((prev) => new Set(prev).add(id));
    try {
      await api.updateFlag(id, status);
      load();
    } catch {
      load();
    } finally {
      setUpdating((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    }
  }

  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 256 }}>
        <Loader2 size={28} style={{ animation: 'spin 0.8s linear infinite', color: 'var(--color-accent-orange)' }} />
      </div>
    );
  }

  if (error) {
    return (
      <div style={{
        borderRadius: 'var(--radius-card)', padding: 24,
        background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)', textAlign: 'center',
      }}>
        <p style={{ color: '#fca5a5', fontWeight: 600 }}>{error}</p>
      </div>
    );
  }

  const openFlags = flags.filter((f) => f.status === 'open');

  return (
    <div className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 800, color: 'var(--color-text-primary)' }}>Flags</h1>
        <p style={{ marginTop: 6, fontSize: '0.875rem', color: 'var(--color-text-muted)' }}>
          Discrepancies detected between expected and actual Sonarr state.
        </p>
      </div>

      <div className="glass-card" style={{ padding: 0, overflow: 'hidden' }}>
        <div style={{ padding: '14px 20px', borderBottom: '1px solid var(--color-glass-border)', display: 'flex', alignItems: 'center', gap: 8 }}>
          <FlagIcon size={14} style={{ color: openFlags.length > 0 ? '#ef4444' : 'var(--color-text-muted)' }} />
          <span className="section-title">
            {openFlags.length === 0 ? 'No open flags' : `${openFlags.length} open flag${openFlags.length !== 1 ? 's' : ''}`}
          </span>
        </div>

        {openFlags.length === 0 ? (
          <div style={{
            padding: '48px 20px', textAlign: 'center',
            color: 'var(--color-text-muted)', fontSize: '0.9rem',
          }}>
            <CheckCircle size={32} style={{ color: 'var(--color-accent-green)', marginBottom: 12, display: 'block', margin: '0 auto 12px' }} />
            No open flags — everything looks good.
          </div>
        ) : (
          <table className="ep-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Show TVDB ID</th>
                <th>Sonarr Episode ID</th>
                <th>Description</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {openFlags.map((flag) => {
                const busy = updating.has(flag.id);
                return (
                  <tr key={flag.id}>
                    <td className="mono" style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>
                      {flag.tvdb_id}
                    </td>
                    <td className="mono" style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>
                      {flag.sonarr_episode_id ?? '—'}
                    </td>
                    <td style={{ fontSize: '0.8rem', color: 'var(--color-text-secondary)' }}>
                      {flag.issue_description}
                    </td>
                    <td style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', whiteSpace: 'nowrap' }}>
                      {formatDate(flag.created_at)}
                    </td>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <button
                          disabled={busy}
                          onClick={() => handleUpdate(flag.id, 'resolved')}
                          style={{
                            display: 'inline-flex', alignItems: 'center', gap: 4,
                            padding: '4px 10px', borderRadius: 'var(--radius-pill)',
                            border: '1px solid rgba(34,197,94,0.3)',
                            background: 'rgba(34,197,94,0.1)',
                            color: '#22c55e', fontSize: '0.72rem', fontWeight: 600,
                            cursor: busy ? 'not-allowed' : 'pointer',
                            opacity: busy ? 0.5 : 1, transition: 'all 0.15s',
                          }}
                        >
                          <CheckCircle size={11} />
                          Resolve
                        </button>
                        <button
                          disabled={busy}
                          onClick={() => handleUpdate(flag.id, 'ignored')}
                          style={{
                            display: 'inline-flex', alignItems: 'center', gap: 4,
                            padding: '4px 10px', borderRadius: 'var(--radius-pill)',
                            border: '1px solid var(--color-glass-border)',
                            background: 'var(--color-glass-bg-light)',
                            color: 'var(--color-text-muted)', fontSize: '0.72rem', fontWeight: 600,
                            cursor: busy ? 'not-allowed' : 'pointer',
                            opacity: busy ? 0.5 : 1, transition: 'all 0.15s',
                          }}
                        >
                          <XCircle size={11} />
                          Ignore
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
