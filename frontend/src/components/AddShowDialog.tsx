import { useState, useEffect, useCallback, useMemo } from 'react';
import { Loader2, Plus, X, HardDrive, Users, RotateCcw } from 'lucide-react';
import { api } from '../api/client';
import type { PlexLibraryShow, ShowPreview, PlexUserOption, UserBufferInfo } from '../api/client';
import { WindowProgress, USER_COLORS } from './ShowCard';

function formatBytes(b: number): string {
  if (b <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  const exp = Math.min(Math.floor(Math.log(b) / Math.log(1024)), units.length - 1);
  const val = b / Math.pow(1024, exp);
  return `${val >= 100 ? val.toFixed(0) : val.toFixed(1)} ${units[exp]}`;
}

interface AddShowDialogProps {
  show: PlexLibraryShow;
  onClose: () => void;
  onAdded: () => void;
}

export function AddShowDialog({ show, onClose, onAdded }: AddShowDialogProps) {
  const [preview, setPreview] = useState<ShowPreview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [users, setUsers] = useState<PlexUserOption[]>([]);
  const [overrideOpen, setOverrideOpen] = useState(false);
  const [overrideUser, setOverrideUser] = useState('');
  const [overrideSeason, setOverrideSeason] = useState(1);
  const [activeSeason, setActiveSeason] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);

  const overrideName = users.find((u) => String(u.id) === overrideUser)?.name ?? '';
  const overrideActive = overrideOpen && overrideUser !== '';

  const loadPreview = useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.getShowPreview(
        show.tvdb_id,
        overrideActive ? { user: overrideUser, name: overrideName, season: overrideSeason } : undefined,
      );
      setPreview(p);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Preview failed');
    }
    setLoading(false);
  }, [show.tvdb_id, overrideActive, overrideUser, overrideName, overrideSeason]);

  useEffect(() => { loadPreview(); }, [loadPreview]);
  useEffect(() => {
    api.getPlexUsers().then(setUsers).catch(() => setUsers([]));
  }, []);

  // One UserBufferInfo per watcher window segment — the shape WindowProgress renders.
  const buffers: UserBufferInfo[] = useMemo(() => {
    if (!preview) return [];
    return preview.watchers.flatMap((w) =>
      w.segments.map((seg) => ({
        display_name: w.display_name,
        season: seg.season,
        buffer_start: seg.start,
        buffer_end: seg.end,
      })),
    );
  }, [preview]);

  const colorMap = useMemo(() => {
    const map: Record<string, string> = {};
    let idx = 0;
    for (const b of buffers) {
      if (!(b.display_name in map)) {
        map[b.display_name] = USER_COLORS[idx % USER_COLORS.length];
        idx++;
      }
    }
    return map;
  }, [buffers]);

  const seasons = preview
    ? Object.keys(preview.all_episodes).map(Number).filter((s) => s > 0).sort((a, b) => a - b)
    : [];
  const firstWindowSeason = buffers.length > 0 ? Math.min(...buffers.map((b) => b.season)) : seasons[0] ?? 1;
  const displaySeason = activeSeason ?? firstWindowSeason;
  const seasonBuffers = buffers.filter((b) => b.season === displaySeason);

  const canAdd = !!preview && (preview.watchers.length > 0 || overrideActive);

  async function handleAdd() {
    setAdding(true);
    try {
      await api.addShow(
        show.tvdb_id,
        overrideActive
          ? { plex_user_id: overrideUser, display_name: overrideName, requested_season: overrideSeason }
          : undefined,
      );
      onAdded();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Add failed');
      setAdding(false);
    }
  }

  const selectStyle: React.CSSProperties = {
    padding: '6px 10px', borderRadius: 8,
    border: '1px solid var(--color-glass-border)',
    background: 'var(--color-glass-bg-light)',
    color: 'var(--color-text-primary)', fontSize: '0.8rem',
  };

  return (
    <div
      style={{
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.65)',
        backdropFilter: 'blur(6px)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        zIndex: 100, padding: 16,
      }}
      onClick={onClose}
    >
      <div
        className="glass-panel"
        style={{ width: 560, maxWidth: '100%', maxHeight: '85vh', overflowY: 'auto', padding: '24px 28px' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 4 }}>
          <h3 style={{ fontSize: '1.1rem', fontWeight: 700, color: 'var(--color-text-primary)' }}>
            Add {show.title}
          </h3>
          <button
            onClick={onClose}
            style={{ background: 'none', border: 'none', color: 'var(--color-text-muted)', cursor: 'pointer', padding: 2 }}
            title="Close"
          >
            <X size={18} />
          </button>
        </div>
        <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)', marginBottom: 18 }}>
          Preview of the watch windows Rollarr would maintain for this show.
        </p>

        {loading && (
          <div style={{ display: 'flex', justifyContent: 'center', padding: '40px 0' }}>
            <Loader2 size={24} style={{ animation: 'spin 0.8s linear infinite', color: 'var(--color-accent-orange)' }} />
          </div>
        )}

        {!loading && error && (
          <div style={{
            borderRadius: 'var(--radius-inner)', padding: 14, marginBottom: 16,
            background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)',
            color: '#fca5a5', fontSize: '0.82rem', fontWeight: 600,
          }}>
            {error}
          </div>
        )}

        {!loading && preview && (
          <>
            {/* Watchers */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
              <Users size={13} style={{ color: 'var(--color-text-muted)' }} />
              <span className="overline" style={{ color: 'var(--color-text-muted)', letterSpacing: '0.1em' }}>
                Watchers
              </span>
            </div>
            {preview.watchers.length === 0 ? (
              <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)', marginBottom: 16 }}>
                No Plex watch history found — pick a user and starting season below to add this show.
              </p>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 16 }}>
                {preview.watchers.map((w) => (
                  <div
                    key={w.plex_user_id}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap',
                      padding: '6px 10px', borderRadius: 'var(--radius-inner)',
                      background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
                      fontSize: '0.78rem',
                    }}
                  >
                    <span style={{
                      width: 16, height: 16, borderRadius: '50%',
                      background: colorMap[w.display_name] ?? '#f97316',
                      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                      fontSize: '0.55rem', fontWeight: 800, color: '#fff', flexShrink: 0,
                    }}>
                      {w.display_name.charAt(0).toUpperCase()}
                    </span>
                    <span style={{ fontWeight: 700, color: 'var(--color-text-primary)' }}>{w.display_name}</span>
                    <span className="mono" style={{ color: 'var(--color-text-muted)' }}>
                      {w.highest_season > 0
                        ? `watched to S${String(w.highest_season).padStart(2, '0')}E${String(w.highest_episode).padStart(2, '0')}`
                        : `starts at S${String(Math.max(1, overrideActive && !w.detected ? overrideSeason : 1)).padStart(2, '0')}E01`}
                    </span>
                    <span style={{ flex: 1 }} />
                    {w.is_rewatching && (
                      <span style={{
                        display: 'inline-flex', alignItems: 'center', gap: 3,
                        fontSize: '0.62rem', fontWeight: 700, padding: '2px 8px',
                        borderRadius: 'var(--radius-pill)',
                        background: 'rgba(168,85,247,0.15)', border: '1px solid rgba(168,85,247,0.3)', color: '#c084fc',
                      }}>
                        <RotateCcw size={9} /> rewatch
                      </span>
                    )}
                    <span style={{
                      fontSize: '0.62rem', fontWeight: 700, padding: '2px 8px',
                      borderRadius: 'var(--radius-pill)',
                      background: w.detected ? 'rgba(34,197,94,0.15)' : 'rgba(249,115,22,0.15)',
                      border: w.detected ? '1px solid rgba(34,197,94,0.25)' : '1px solid rgba(249,115,22,0.3)',
                      color: w.detected ? '#22c55e' : 'var(--color-accent-orange)',
                    }}>
                      {w.detected ? 'detected' : 'manual'}
                    </span>
                  </div>
                ))}
              </div>
            )}

            {/* Window preview */}
            {seasons.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexWrap: 'wrap', marginBottom: 8 }}>
                  {seasons.map((sn) => {
                    const isActive = sn === displaySeason;
                    const hasWindow = buffers.some((b) => b.season === sn);
                    return (
                      <button
                        key={sn}
                        onClick={() => setActiveSeason(sn)}
                        style={{
                          padding: '2px 8px', borderRadius: 'var(--radius-pill)',
                          border: isActive ? '1px solid rgba(249,115,22,0.5)' : '1px solid var(--color-glass-border)',
                          background: isActive ? 'rgba(249,115,22,0.15)' : 'var(--color-glass-bg-light)',
                          color: isActive ? 'var(--color-accent-orange)' : 'var(--color-text-muted)',
                          fontSize: '0.68rem', fontWeight: 700, cursor: 'pointer',
                          fontVariantNumeric: 'tabular-nums',
                          opacity: hasWindow || isActive ? 1 : 0.55,
                        }}
                      >
                        S{String(sn).padStart(2, '0')}
                      </button>
                    );
                  })}
                </div>
                <WindowProgress
                  seasonBuffers={seasonBuffers}
                  allBuffers={buffers}
                  viewingSeason={displaySeason}
                  colorMap={colorMap}
                  totalEpisodes={preview.all_episodes[displaySeason]?.length}
                />
              </div>
            )}

            {/* Savings */}
            <div style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '10px 14px', borderRadius: 'var(--radius-inner)', marginBottom: 16,
              background: 'rgba(249,115,22,0.08)', border: '1px solid rgba(249,115,22,0.2)',
            }}>
              <HardDrive size={15} style={{ color: 'var(--color-accent-orange)', flexShrink: 0 }} />
              <span style={{ fontSize: '0.82rem', color: 'var(--color-text-secondary)' }}>
                Adding frees{' '}
                <strong className="mono" style={{ color: 'var(--color-accent-orange)' }}>
                  {formatBytes(preview.bytes_freed)}
                </strong>
                {' '}({preview.files_deleted} episode file{preview.files_deleted === 1 ? '' : 's'} outside the windows)
              </span>
            </div>

            {/* Override */}
            <div style={{ marginBottom: 20 }}>
              <button
                onClick={() => setOverrideOpen((o) => !o)}
                style={{
                  background: 'none', border: 'none', padding: 0, cursor: 'pointer',
                  fontSize: '0.78rem', fontWeight: 600,
                  color: overrideOpen ? 'var(--color-accent-orange)' : 'var(--color-text-muted)',
                }}
              >
                {overrideOpen ? '▾' : '▸'} Override watcher
              </button>
              {overrideOpen && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginTop: 10 }}>
                  <select value={overrideUser} onChange={(e) => setOverrideUser(e.target.value)} style={selectStyle}>
                    <option value="">Select user…</option>
                    {users.map((u) => (
                      <option key={u.id} value={String(u.id)}>{u.name}</option>
                    ))}
                  </select>
                  <span style={{ fontSize: '0.78rem', color: 'var(--color-text-muted)' }}>starting at</span>
                  <select
                    value={overrideSeason}
                    onChange={(e) => setOverrideSeason(Number(e.target.value))}
                    style={selectStyle}
                  >
                    {(seasons.length > 0 ? seasons : [1]).map((sn) => (
                      <option key={sn} value={sn}>Season {sn}</option>
                    ))}
                  </select>
                </div>
              )}
            </div>
          </>
        )}

        {/* Actions */}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10 }}>
          <button
            onClick={onClose}
            style={{
              padding: '8px 16px', borderRadius: 'var(--radius-pill)',
              border: '1px solid var(--color-glass-border)',
              background: 'var(--color-glass-bg-light)',
              color: 'var(--color-text-secondary)',
              fontSize: '0.875rem', fontWeight: 600, cursor: 'pointer',
            }}
          >
            Cancel
          </button>
          <button
            onClick={handleAdd}
            disabled={!canAdd || adding}
            title={!canAdd ? 'No watch history — pick a user and season first' : undefined}
            style={{
              display: 'inline-flex', alignItems: 'center', gap: 6,
              padding: '8px 16px', borderRadius: 'var(--radius-pill)',
              border: '1px solid rgba(34,197,94,0.4)',
              background: 'rgba(34,197,94,0.15)',
              color: '#4ade80',
              fontSize: '0.875rem', fontWeight: 600,
              cursor: !canAdd || adding ? 'not-allowed' : 'pointer',
              opacity: !canAdd || adding ? 0.5 : 1,
            }}
          >
            {adding
              ? <Loader2 size={14} style={{ animation: 'spin 0.8s linear infinite' }} />
              : <Plus size={14} />}
            Add to Rollarr
          </button>
        </div>
      </div>
    </div>
  );
}
