import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { Loader2, Search, Plus, Check, AlertTriangle, HardDrive, RotateCcw } from 'lucide-react';
import { api } from '../api/client';
import type { PlexLibraryShow, ShowPreview, PlexUserOption, UserBufferInfo } from '../api/client';
import { ShowPoster, WindowProgress, USER_COLORS } from '../components/ShowCard';
import { useSSE } from '../hooks/useSSE';

function formatBytes(b: number): string {
  if (b <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  const exp = Math.min(Math.floor(Math.log(b) / Math.log(1024)), units.length - 1);
  const val = b / Math.pow(1024, exp);
  return `${val >= 100 ? val.toFixed(0) : val.toFixed(1)} ${units[exp]}`;
}

// Bound concurrent preview fetches so a large library doesn't flood Plex/Sonarr.
function makeLimiter(max: number) {
  let active = 0;
  const queue: Array<() => void> = [];
  const pump = () => {
    if (active >= max || queue.length === 0) return;
    active++;
    queue.shift()!();
  };
  return function run<T>(fn: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      queue.push(() => {
        fn().then(resolve, reject).finally(() => { active--; pump(); });
      });
      pump();
    });
  };
}
const previewLimit = makeLimiter(4);

export function LibraryPage() {
  const [shows, setShows] = useState<PlexLibraryShow[]>([]);
  const [users, setUsers] = useState<PlexUserOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('');

  const load = useCallback(async () => {
    try {
      setShows(await api.getLibrary());
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load Plex library');
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);
  useEffect(() => { api.getPlexUsers().then(setUsers).catch(() => setUsers([])); }, []);
  useSSE(load);

  const filtered = query
    ? shows.filter((s) => s.title.toLowerCase().includes(query.toLowerCase()))
    : shows;

  const addable = shows.filter((s) => s.in_sonarr && !s.is_tracked).length;

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
        background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.2)',
        textAlign: 'center',
      }}>
        <p style={{ color: '#fca5a5', fontWeight: 600 }}>{error}</p>
        <p style={{ fontSize: '0.85rem', marginTop: 4, color: 'var(--color-text-muted)' }}>
          Check your Plex connection and settings.
        </p>
      </div>
    );
  }

  return (
    <div className="page-enter" style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: '1.15rem', fontWeight: 700, color: 'var(--color-text-primary)', margin: 0 }}>
            Plex Library
          </h2>
          <p style={{ fontSize: '0.78rem', color: 'var(--color-text-muted)', margin: '2px 0 0' }}>
            {shows.length} shows · {addable} available to add
          </p>
        </div>
        <span style={{ flex: 1 }} />
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '7px 12px', borderRadius: 'var(--radius-pill)',
          background: 'var(--color-glass-bg-light)', border: '1px solid var(--color-glass-border)',
          minWidth: 220,
        }}>
          <Search size={14} style={{ color: 'var(--color-text-muted)', flexShrink: 0 }} />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search shows…"
            style={{
              background: 'none', border: 'none', outline: 'none',
              color: 'var(--color-text-primary)', fontSize: '0.82rem', width: '100%',
            }}
          />
        </div>
      </div>

      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(440px, 1fr))',
        gap: 14, alignItems: 'stretch',
      }}>
        {filtered.map((show) => (
          <LibraryCard key={show.plex_key} show={show} users={users} onChanged={load} />
        ))}
      </div>

      {filtered.length === 0 && (
        <p style={{ textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.85rem', padding: '40px 0' }}>
          No shows match “{query}”.
        </p>
      )}
    </div>
  );
}

// ── Library card ────────────────────────────────────────────────────────────────
function LibraryCard({ show, users, onChanged }: {
  show: PlexLibraryShow;
  users: PlexUserOption[];
  onChanged: () => void;
}) {
  const navigate = useNavigate();
  const addable = show.in_sonarr && !show.is_tracked;

  const cardRef = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [preview, setPreview] = useState<ShowPreview | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [activeSeason, setActiveSeason] = useState<number | null>(null);

  const [overrideUser, setOverrideUser] = useState('');
  const [overrideSeason, setOverrideSeason] = useState(1);
  const overrideName = users.find((u) => String(u.id) === overrideUser)?.name ?? '';
  const overrideActive = overrideUser !== '';

  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);

  // Lazily fetch the preview only once the card scrolls into view.
  useEffect(() => {
    if (!addable || !cardRef.current) return;
    const el = cardRef.current;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        setVisible(true);
        io.disconnect();
      }
    }, { rootMargin: '200px' });
    io.observe(el);
    return () => io.disconnect();
  }, [addable]);

  const fetchPreview = useCallback(() => {
    setPreview(null);
    setPreviewError(null);
    previewLimit(() => api.getShowPreview(show.tvdb_id, {
      key: show.plex_key,
      override: overrideActive ? { user: overrideUser, name: overrideName, season: overrideSeason } : undefined,
    }))
      .then(setPreview)
      .catch((err) => setPreviewError(err instanceof Error ? err.message : 'Preview failed'));
  }, [show.tvdb_id, show.plex_key, overrideActive, overrideUser, overrideName, overrideSeason]);

  useEffect(() => { if (visible) fetchPreview(); }, [visible, fetchPreview]);

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
  const firstWindowSeason = buffers.length > 0 ? Math.min(...buffers.map((b) => b.season)) : (seasons[0] ?? 1);
  const displaySeason = activeSeason ?? firstWindowSeason;
  const seasonBuffers = buffers.filter((b) => b.season === displaySeason);

  const canAdd = !!preview && (preview.watchers.length > 0 || overrideActive);

  async function handleAdd() {
    setAdding(true);
    setAddError(null);
    try {
      await api.addShow(
        show.tvdb_id,
        overrideActive
          ? { plex_user_id: overrideUser, display_name: overrideName, requested_season: overrideSeason }
          : undefined,
      );
      onChanged();
    } catch (err) {
      setAddError(err instanceof Error ? err.message : 'Add failed');
      setAdding(false);
    }
  }

  const selectStyle: React.CSSProperties = {
    padding: '5px 8px', borderRadius: 7,
    border: '1px solid var(--color-glass-border)',
    background: 'var(--color-glass-bg-light)',
    color: 'var(--color-text-primary)', fontSize: '0.76rem',
  };

  // ── Non-addable: compact row (tracked or not in Sonarr) ──
  if (!addable) {
    const disabled = !show.in_sonarr;
    return (
      <div
        className="glass-card"
        style={{ display: 'flex', gap: 12, padding: 12, opacity: disabled ? 0.55 : 1, cursor: show.is_tracked ? 'pointer' : 'default' }}
        onClick={() => { if (show.is_tracked) navigate(`/shows/${show.tvdb_id}`); }}
      >
        <div style={{ width: 48, flexShrink: 0, borderRadius: 8, overflow: 'hidden', position: 'relative' }}>
          <ShowPoster title={show.title} posterUrl={show.poster_url} />
        </div>
        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 6 }}>
          <CardTitle show={show} />
          {show.is_tracked
            ? <Badge color="#22c55e" bg="rgba(34,197,94,0.15)" border="rgba(34,197,94,0.25)" icon={<Check size={10} />} label="Tracked" />
            : <Badge color="#f59e0b" bg="rgba(245,158,11,0.15)" border="rgba(245,158,11,0.25)" icon={<AlertTriangle size={10} />} label="Not in Sonarr" title="Rollarr can only manage shows Sonarr knows about" />}
        </div>
      </div>
    );
  }

  // ── Addable: full inline preview ──
  return (
    <div ref={cardRef} className="glass-card" style={{ display: 'flex', flexDirection: 'column', padding: 14, gap: 12 }}>
      <div style={{ display: 'flex', gap: 12 }}>
        <div style={{ width: 56, flexShrink: 0, borderRadius: 8, overflow: 'hidden', position: 'relative' }}>
          <ShowPoster title={show.title} posterUrl={show.poster_url} />
        </div>
        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 8 }}>
          <CardTitle show={show} />
          {/* Savings */}
          {preview && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: '0.78rem', color: 'var(--color-text-secondary)' }}>
              <HardDrive size={13} style={{ color: 'var(--color-accent-orange)', flexShrink: 0 }} />
              <span>
                frees{' '}
                <strong className="mono" style={{ color: 'var(--color-accent-orange)' }}>{formatBytes(preview.bytes_freed)}</strong>
                {' '}· {preview.files_deleted} file{preview.files_deleted === 1 ? '' : 's'}
              </span>
            </div>
          )}
          {/* Watcher chips */}
          {preview && preview.watchers.length > 0 && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5 }}>
              {preview.watchers.map((wch) => (
                <span
                  key={wch.plex_user_id}
                  title={wch.highest_season > 0 ? `watched to S${String(wch.highest_season).padStart(2, '0')}E${String(wch.highest_episode).padStart(2, '0')}` : 'no history'}
                  style={{
                    display: 'inline-flex', alignItems: 'center', gap: 4,
                    padding: '2px 8px 2px 3px', borderRadius: 'var(--radius-pill)',
                    background: `${colorMap[wch.display_name] ?? '#f97316'}15`,
                    border: `1px solid ${colorMap[wch.display_name] ?? '#f97316'}40`,
                    fontSize: '0.62rem', fontWeight: 700, color: colorMap[wch.display_name] ?? '#f97316',
                  }}
                >
                  <span style={{
                    width: 14, height: 14, borderRadius: '50%', background: colorMap[wch.display_name] ?? '#f97316',
                    display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                    fontSize: '0.5rem', fontWeight: 800, color: '#fff',
                  }}>
                    {wch.display_name.charAt(0).toUpperCase()}
                  </span>
                  {wch.display_name}
                  {wch.is_rewatching && <RotateCcw size={9} />}
                </span>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Preview body */}
      {!preview && !previewError && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 0', color: 'var(--color-text-muted)', fontSize: '0.78rem' }}>
          <Loader2 size={14} style={{ animation: 'spin 0.8s linear infinite' }} /> Computing windows…
        </div>
      )}
      {previewError && (
        <div style={{ fontSize: '0.76rem', color: '#fca5a5' }}>{previewError}</div>
      )}

      {preview && (
        <>
          {preview.watchers.length === 0 && !overrideActive && (
            <p style={{ fontSize: '0.76rem', color: 'var(--color-text-muted)', margin: 0 }}>
              No watch history — pick a user and starting season to add.
            </p>
          )}

          {seasons.length > 0 && (preview.watchers.length > 0 || overrideActive) && (
            <div>
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
                        fontSize: '0.66rem', fontWeight: 700, cursor: 'pointer',
                        fontVariantNumeric: 'tabular-nums', opacity: hasWindow || isActive ? 1 : 0.5,
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

          {/* Override + Add */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginTop: 2 }}>
            <select value={overrideUser} onChange={(e) => setOverrideUser(e.target.value)} style={selectStyle}>
              <option value="">{preview.watchers.length > 0 ? 'Override user…' : 'Select user…'}</option>
              {users.map((u) => <option key={u.id} value={String(u.id)}>{u.name}</option>)}
            </select>
            {overrideActive && (
              <select value={overrideSeason} onChange={(e) => setOverrideSeason(Number(e.target.value))} style={selectStyle}>
                {(seasons.length > 0 ? seasons : [1]).map((sn) => <option key={sn} value={sn}>Season {sn}</option>)}
              </select>
            )}
            <span style={{ flex: 1 }} />
            {addError && <span style={{ fontSize: '0.7rem', color: '#fca5a5' }}>{addError}</span>}
            <button
              onClick={handleAdd}
              disabled={!canAdd || adding}
              title={!canAdd ? 'Pick a user and season first' : undefined}
              style={{
                display: 'inline-flex', alignItems: 'center', gap: 5,
                padding: '6px 14px', borderRadius: 'var(--radius-pill)',
                border: '1px solid rgba(34,197,94,0.4)', background: 'rgba(34,197,94,0.15)',
                color: '#4ade80', fontSize: '0.8rem', fontWeight: 600,
                cursor: !canAdd || adding ? 'not-allowed' : 'pointer', opacity: !canAdd || adding ? 0.5 : 1,
              }}
            >
              {adding ? <Loader2 size={13} style={{ animation: 'spin 0.8s linear infinite' }} /> : <Plus size={13} />}
              Add
            </button>
          </div>
        </>
      )}
    </div>
  );
}

function CardTitle({ show }: { show: PlexLibraryShow }) {
  return (
    <div style={{ fontWeight: 700, fontSize: '0.88rem', color: 'var(--color-text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
      {show.title}
      {show.year > 0 && (
        <span style={{ fontWeight: 500, color: 'var(--color-text-muted)', marginLeft: 6, fontSize: '0.76rem' }}>{show.year}</span>
      )}
    </div>
  );
}

function Badge({ color, bg, border, icon, label, title }: {
  color: string; bg: string; border: string; icon: React.ReactNode; label: string; title?: string;
}) {
  return (
    <span
      title={title}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 4, width: 'fit-content',
        fontSize: '0.65rem', fontWeight: 700, padding: '2px 8px',
        borderRadius: 'var(--radius-pill)', background: bg, border: `1px solid ${border}`, color,
      }}
    >
      {icon} {label}
    </span>
  );
}
