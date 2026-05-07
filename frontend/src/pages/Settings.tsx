import { useState, useEffect } from 'react';
import { Save, Loader2, CheckCircle, AlertCircle, Eye, EyeOff } from 'lucide-react';
import { api } from '../api/client';
import type { SettingsMap } from '../api/client';

const SECRET_SENTINEL = '***SET***';

interface Field {
  key: string;
  label: string;
  placeholder?: string;
  type?: 'text' | 'password' | 'number';
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
      { key: 'plex_url',     label: 'Plex URL',            placeholder: 'http://plex:32400', type: 'text' },
      { key: 'plex_token',   label: 'Plex Token',          type: 'password', hint: 'Admin token — covers all user types' },
      { key: 'plex_db_path', label: 'Plex Database Path',  placeholder: '/var/lib/plexmediaserver/…', type: 'text',
        hint: 'Optional: path to Plex SQLite DB. Enables "Mark as Watched" tracking.' },
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
      { key: 'global_buffer_size',           label: 'Buffer size (episodes)',          type: 'number', hint: 'Episodes kept on disk ahead of each active request' },
      { key: 'global_inactivity_days',        label: 'Inactivity threshold (days)',     type: 'number', hint: 'Show marked inactive after this many days without activity' },
      { key: 'reconcile_interval_minutes',   label: 'Reconcile interval (minutes)',    type: 'number', hint: 'Requires restart to apply changes' },
      { key: 'inactivity_interval_minutes',  label: 'Inactivity check interval (minutes)', type: 'number', hint: 'Requires restart to apply changes' },
    ],
  },
];

const SECRET_KEYS = new Set(['sonarr_api_key', 'plex_token', 'seerr_webhook_secret']);

export function Settings() {
  const [values, setValues] = useState<SettingsMap>({});
  // Track which secret fields the user has typed a new value into
  const [secretEdits, setSecretEdits] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Set<string>>(new Set());

  useEffect(() => {
    api.getSettings().then((data) => {
      setValues(data);
      setLoading(false);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : 'Failed to load settings');
      setLoading(false);
    });
  }, []);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError(null);

    // Build payload: for secrets, only send new value if user typed something; otherwise send sentinel
    const payload: SettingsMap = { ...values };
    for (const key of SECRET_KEYS) {
      if (secretEdits[key] !== undefined && secretEdits[key] !== '') {
        payload[key] = secretEdits[key];
      } else if (values[key] === SECRET_SENTINEL) {
        payload[key] = SECRET_SENTINEL; // preserve existing secret
      }
    }

    try {
      const updated = await api.saveSettings(payload);
      setValues(updated);
      setSecretEdits({});
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings');
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
    if (SECRET_KEYS.has(key)) {
      setSecretEdits((prev) => ({ ...prev, [key]: val }));
    } else {
      setValues((v) => ({ ...v, [key]: val }));
    }
  }

  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 256 }}>
        <Loader2 size={28} style={{ animation: 'spin 0.8s linear infinite', color: 'var(--color-accent-orange)' }} />
      </div>
    );
  }

  return (
    <form
      onSubmit={handleSave}
      className="page-enter"
      style={{ maxWidth: 640, display: 'flex', flexDirection: 'column', gap: 20 }}
    >
      <div>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 800, color: 'var(--color-text-primary)' }}>Settings</h1>
        <p style={{ marginTop: 6, fontSize: '0.875rem', color: 'var(--color-text-muted)' }}>
          Configuration is stored in the database — changes take effect on the next reconcile cycle.
        </p>
      </div>

      {FIELD_GROUPS.map((group) => (
        <div key={group.title} className="settings-group">
          <div className="settings-group-header">
            <span className="section-title">{group.title}</span>
          </div>
          <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 16 }}>
            {group.fields.map((field) => {
              const isSecret = SECRET_KEYS.has(field.key);
              const isPassword = field.type === 'password';
              const show = revealed.has(field.key);

              // For secrets: show the edit value if user typed something, else show empty
              // (placeholder explains the sentinel)
              const inputValue = isSecret
                ? (secretEdits[field.key] ?? '')
                : (values[field.key] ?? '');

              const isAlreadySet = isSecret && values[field.key] === SECRET_SENTINEL;

              return (
                <div key={field.key}>
                  <label style={{ display: 'block', fontSize: '0.82rem', fontWeight: 600, marginBottom: 6, color: 'var(--color-text-secondary)' }}>
                    {field.label}
                  </label>
                  <div style={{ position: 'relative' }}>
                    <input
                      type={isPassword ? (show ? 'text' : 'password') : (field.type ?? 'text')}
                      value={inputValue}
                      onChange={(e) => set(field.key, e.target.value)}
                      placeholder={isAlreadySet ? 'already set — enter new value to change' : field.placeholder}
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
            Changes will apply on the next reconcile cycle.
          </p>
        )}
      </div>
    </form>
  );
}
