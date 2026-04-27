import { useState, useEffect } from 'react';
import { Save, Loader2, CheckCircle, AlertCircle, Eye, EyeOff, ShieldAlert } from 'lucide-react';
import { api } from '../api/client';
import type { SettingsMap } from '../api/client';

interface Field {
  key: string;
  label: string;
  placeholder?: string;
  type?: 'text' | 'password' | 'number' | 'toggle';
  hint?: string;
}

const FIELD_GROUPS: Array<{ title: string; fields: Field[] }> = [
  {
    title: 'Sonarr',
    fields: [
      { key: 'sonarr_url',     label: 'Sonarr URL',  placeholder: 'http://sonarr:8989', type: 'text' },
      { key: 'sonarr_api_key', label: 'API Key',      type: 'password' },
    ],
  },
  {
    title: 'Plex',
    fields: [
      { key: 'plex_url',    label: 'Plex URL',            placeholder: 'http://plex:32400', type: 'text' },
      { key: 'plex_token',  label: 'Plex Token',          type: 'password', hint: 'Admin token — covers all user types' },
      { key: 'plex_db_path', label: 'Plex Database Path', placeholder: '/var/lib/plexmediaserver/…', type: 'text',
        hint: 'Optional: path to Plex SQLite DB. Enables "Mark as Watched" tracking for all users including remote friends.' },
    ],
  },
  {
    title: 'Webhooks',
    fields: [
      { key: 'seerr_webhook_secret', label: 'Seerr Webhook Secret', type: 'password',
        hint: 'Set this in Seerr → Notifications → Webhook → Custom header: x-webhook-secret' },
    ],
  },
  {
    title: 'Behaviour',
    fields: [
      { key: 'buffer_size',                  label: 'Buffer size (episodes)',          type: 'number', hint: 'Episodes kept on disk ahead of each active tracker' },
      { key: 'starter_buffer_size',          label: 'Starter buffer (episodes)',       type: 'number', hint: 'Episodes always kept from the start of a show so new watchers can begin immediately' },
      { key: 'inactivity_warn_days',         label: 'Inactivity warning (days)',       type: 'number' },
      { key: 'inactivity_remove_days',       label: 'Inactivity removal (days)',       type: 'number', hint: 'Tracker removed and files deleted after this many inactive days' },
      { key: 'poll_interval_minutes',        label: 'Poll interval (minutes)',         type: 'number', hint: 'Restart required to apply changes' },
      { key: 'maintenance_interval_minutes', label: 'Maintenance interval (minutes)', type: 'number', hint: 'Restart required to apply changes' },
      { key: 'cancel_queued_downloads', label: 'Cancel queued downloads', type: 'toggle', hint: 'Remove episodes outside the buffer from the download queue when a show is bootstrapped.' },
      { key: 'dry_mode', label: 'Dry mode', type: 'toggle', hint: 'Log deletions without executing them. Files are never removed while this is on.' },
    ],
  },
];

export function Settings() {
  const [values, setValues] = useState<SettingsMap>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Set<string>>(new Set());

  useEffect(() => {
    api.getSettings().then((result) => {
      if ('data' in result) setValues(result.data);
      else setError(result.error);
      setLoading(false);
    });
  }, []);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError(null);
    const result = await api.saveSettings(values);
    if ('error' in result) {
      setError(result.error);
    } else {
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    }
    setSaving(false);
  }

  function toggleReveal(key: string) {
    setRevealed((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  }

  function set(key: string, val: string) {
    setValues((v) => ({ ...v, [key]: val }));
  }

  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 256 }}>
        <Loader2 size={28} style={{ animation: 'spin 0.8s linear infinite', color: 'var(--color-accent-orange)' }} />
      </div>
    );
  }

  const dryModeActive = values['dry_mode'] === 'true';

  return (
    <form
      onSubmit={handleSave}
      className="page-enter"
      style={{ maxWidth: 640, display: 'flex', flexDirection: 'column', gap: 20 }}
    >
      <div>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 800, color: 'var(--color-text-primary)' }}>Settings</h1>
        <p style={{ marginTop: 6, fontSize: '0.875rem', color: 'var(--color-text-muted)' }}>
          Configuration is stored in the database — changes take effect on the next poll.
        </p>
      </div>

      {dryModeActive && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px',
          borderRadius: 'var(--radius-card)',
          background: 'rgba(245,158,11,0.1)', border: '1px solid rgba(245,158,11,0.3)',
        }}>
          <ShieldAlert size={16} style={{ color: 'var(--color-accent-amber)', flexShrink: 0 }} />
          <p style={{ color: '#fcd34d', fontWeight: 600, fontSize: '0.85rem' }}>
            Dry mode is active — no files will be deleted.
          </p>
        </div>
      )}

      {FIELD_GROUPS.map((group) => (
        <div key={group.title} className="settings-group">
          <div className="settings-group-header">
            <span className="section-title">{group.title}</span>
          </div>
          <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 16 }}>
            {group.fields.map((field) => {
              if (field.type === 'toggle') {
                const isOn = values[field.key] === 'true';
                return (
                  <div key={field.key} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
                    <div>
                      <div style={{ fontWeight: 600, fontSize: '0.85rem', color: 'var(--color-text-secondary)' }}>{field.label}</div>
                      {field.hint && <div style={{ fontSize: '0.72rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{field.hint}</div>}
                    </div>
                    <button
                      type="button"
                      className="toggle-track"
                      onClick={() => set(field.key, isOn ? 'false' : 'true')}
                      style={{
                        background: isOn ? 'var(--color-accent-orange)' : 'rgba(255,255,255,0.1)',
                        boxShadow: isOn ? '0 0 12px rgba(249,115,22,0.3)' : 'none',
                      }}
                    >
                      <span className="toggle-thumb" style={{ left: isOn ? 24 : 4 }} />
                    </button>
                  </div>
                );
              }

              const isPassword = field.type === 'password';
              const show = revealed.has(field.key);
              return (
                <div key={field.key}>
                  <label style={{ display: 'block', fontSize: '0.82rem', fontWeight: 600, marginBottom: 6, color: 'var(--color-text-secondary)' }}>
                    {field.label}
                  </label>
                  <div style={{ position: 'relative' }}>
                    <input
                      type={isPassword ? (show ? 'text' : 'password') : (field.type ?? 'text')}
                      value={values[field.key] ?? ''}
                      onChange={(e) => set(field.key, e.target.value)}
                      placeholder={field.placeholder}
                      className="glass-input"
                      style={{ paddingRight: isPassword ? 40 : 14 }}
                    />
                    {isPassword && (
                      <button
                        type="button"
                        onClick={() => toggleReveal(field.key)}
                        style={{
                          position: 'absolute', right: 12, top: '50%', transform: 'translateY(-50%)',
                          background: 'none', border: 'none', cursor: 'pointer',
                          color: 'var(--color-text-muted)', padding: 2,
                        }}
                      >
                        {show ? <EyeOff size={14} /> : <Eye size={14} />}
                      </button>
                    )}
                  </div>
                  {field.hint && <div style={{ fontSize: '0.72rem', color: 'var(--color-text-muted)', marginTop: 5 }}>{field.hint}</div>}
                </div>
              );
            })}
          </div>
        </div>
      ))}

      {error && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8, padding: '12px 16px',
          borderRadius: 'var(--radius-card)',
          background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.2)',
          color: '#fca5a5', fontSize: '0.875rem',
        }}>
          <AlertCircle size={16} />
          {error}
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
        <button
          type="submit"
          disabled={saving}
          style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '0 20px', height: 36, borderRadius: 'var(--radius-pill)',
            border: 'none', cursor: saving ? 'not-allowed' : 'pointer',
            background: saved ? 'var(--color-accent-green)' : 'var(--color-accent-orange)',
            color: '#fff', fontWeight: 600, fontSize: '0.875rem',
            opacity: saving ? 0.7 : 1,
            boxShadow: saved ? '0 2px 12px rgba(34,197,94,0.25)' : '0 2px 12px rgba(249,115,22,0.25)',
            transition: 'all 0.18s',
          }}
        >
          {saving
            ? <><Loader2 size={14} style={{ animation: 'spin 0.8s linear infinite' }} /> Saving…</>
            : saved
            ? <><CheckCircle size={14} /> Settings saved!</>
            : <><Save size={14} /> Save settings</>
          }
        </button>
        {saved && (
          <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>
            Changes will apply on the next poll cycle.
          </p>
        )}
      </div>
    </form>
  );
}
