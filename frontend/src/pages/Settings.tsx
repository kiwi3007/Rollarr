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
      { key: 'sonarr_url',     label: 'Sonarr URL',     placeholder: 'http://sonarr:8989', type: 'text' },
      { key: 'sonarr_api_key', label: 'API Key',         type: 'password' },
    ],
  },
  {
    title: 'Plex',
    fields: [
      { key: 'plex_url',   label: 'Plex URL',   placeholder: 'http://plex:32400', type: 'text' },
      { key: 'plex_token', label: 'Plex Token', type: 'password', hint: 'Admin token — covers all user types' },
      {
        key: 'plex_db_path',
        label: 'Plex Database Path',
        placeholder: '/var/lib/plexmediaserver/Library/Application Support/Plex Media Server/Plug-in Support/Databases/com.plexapp.plugins.library.db',
        type: 'text',
        hint: 'Optional: path to Plex SQLite DB. Enables "Mark as Watched" tracking for all users including remote friends.',
      },
    ],
  },
  {
    title: 'Webhooks',
    fields: [
      {
        key: 'seerr_webhook_secret',
        label: 'Seerr Webhook Secret',
        type: 'password',
        hint: 'Set this in Seerr → Notifications → Webhook → Custom header: x-webhook-secret',
      },
    ],
  },
  {
    title: 'Behaviour',
    fields: [
      { key: 'buffer_size',                  label: 'Buffer Size (episodes)',               type: 'number', hint: 'Episodes kept on disk ahead of each active tracker' },
      { key: 'starter_buffer_size',          label: 'Starter Buffer (episodes)',            type: 'number', hint: 'Episodes always kept from the start of a show so new watchers can begin immediately' },
      { key: 'inactivity_warn_days',         label: 'Inactivity Warning (days)',            type: 'number' },
      { key: 'inactivity_remove_days',       label: 'Inactivity Removal (days)',            type: 'number', hint: 'Tracker removed and files deleted after this many inactive days' },
      { key: 'poll_interval_minutes',        label: 'Poll Interval (minutes)',              type: 'number', hint: 'Restart required to apply changes' },
      { key: 'maintenance_interval_minutes', label: 'Maintenance Interval (minutes)',       type: 'number', hint: 'Restart required to apply changes' },
      { key: 'cancel_queued_downloads', label: 'Cancel Queued Downloads', type: 'toggle', hint: 'Remove episodes outside the buffer from the download queue when a show is bootstrapped.' },
      { key: 'dry_mode', label: 'Dry Mode', type: 'toggle', hint: 'Log deletions without executing them. Files are never removed while this is on.' },
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
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 size={28} className="animate-spin" style={{ color: 'var(--emerald)' }} />
      </div>
    );
  }

  const dryModeActive = values['dry_mode'] === 'true';

  return (
    <form onSubmit={handleSave} className="max-w-2xl space-y-6 fade-up">
      <div>
        <h1 className="text-2xl font-extrabold text-white">Settings</h1>
        <p className="text-sm mt-1" style={{ color: 'rgba(255,255,255,0.4)' }}>
          Configuration is stored in the database — changes take effect on the next poll.
        </p>
      </div>

      {dryModeActive && (
        <div
          className="flex items-center gap-3 px-4 py-3 rounded-xl"
          style={{ background: 'rgba(245,158,11,0.1)', border: '1px solid rgba(245,158,11,0.3)' }}
        >
          <ShieldAlert size={16} style={{ color: 'var(--amber)', flexShrink: 0 }} />
          <p className="text-sm font-medium" style={{ color: '#fcd34d' }}>
            Dry mode is active — no files will be deleted.
          </p>
        </div>
      )}

      {FIELD_GROUPS.map((group) => (
        <div
          key={group.title}
          className="rounded-xl overflow-hidden"
          style={{ background: 'var(--bg-card)', border: '1px solid var(--border)' }}
        >
          <div
            className="px-5 py-3"
            style={{ borderBottom: '1px solid rgba(255,255,255,0.05)' }}
          >
            <h2 className="text-sm font-bold tracking-wider uppercase" style={{ color: 'rgba(255,255,255,0.5)' }}>
              {group.title}
            </h2>
          </div>
          <div className="p-5 space-y-4">
            {group.fields.map((field) => {
              const isToggle = field.type === 'toggle';
              if (isToggle) {
                const isOn = values[field.key] === 'true';
                return (
                  <div key={field.key} className="flex items-center justify-between gap-4">
                    <div>
                      <p className="text-sm font-semibold" style={{ color: 'rgba(255,255,255,0.7)' }}>{field.label}</p>
                      {field.hint && <p className="text-xs mt-0.5" style={{ color: 'rgba(255,255,255,0.3)' }}>{field.hint}</p>}
                    </div>
                    <button
                      type="button"
                      onClick={() => setValues((v) => ({ ...v, [field.key]: isOn ? 'false' : 'true' }))}
                      className="relative shrink-0 w-11 h-6 rounded-full transition-all duration-200"
                      style={{
                        background: isOn ? 'var(--amber)' : 'rgba(255,255,255,0.1)',
                        boxShadow: isOn ? '0 0 12px rgba(245,158,11,0.4)' : 'none',
                      }}
                    >
                      <span
                        className="absolute top-1 w-4 h-4 rounded-full transition-all duration-200"
                        style={{
                          background: 'white',
                          left: isOn ? '24px' : '4px',
                        }}
                      />
                    </button>
                  </div>
                );
              }

              const isPassword = field.type === 'password';
              const show = revealed.has(field.key);
              const inputType = isPassword ? (show ? 'text' : 'password') : (field.type ?? 'text');
              return (
                <div key={field.key}>
                  <label
                    htmlFor={field.key}
                    className="block text-sm font-semibold mb-1.5"
                    style={{ color: 'rgba(255,255,255,0.7)' }}
                  >
                    {field.label}
                  </label>
                  <div className="relative">
                    <input
                      id={field.key}
                      type={inputType}
                      value={values[field.key] ?? ''}
                      onChange={(e) => setValues((v) => ({ ...v, [field.key]: e.target.value }))}
                      placeholder={field.placeholder}
                      className="w-full rounded-lg px-4 py-2.5 text-sm mono outline-none transition-all"
                      style={{
                        background: 'rgba(255,255,255,0.04)',
                        border: '1px solid rgba(255,255,255,0.08)',
                        color: '#e8e8f0',
                      }}
                      onFocus={(e) => {
                        (e.target as HTMLInputElement).style.borderColor = 'rgba(16,185,129,0.4)';
                        (e.target as HTMLInputElement).style.boxShadow = '0 0 0 3px rgba(16,185,129,0.06)';
                      }}
                      onBlur={(e) => {
                        (e.target as HTMLInputElement).style.borderColor = 'rgba(255,255,255,0.08)';
                        (e.target as HTMLInputElement).style.boxShadow = '';
                      }}
                    />
                    {isPassword && (
                      <button
                        type="button"
                        onClick={() => toggleReveal(field.key)}
                        className="absolute right-3 top-1/2 -translate-y-1/2"
                        style={{ color: 'rgba(255,255,255,0.3)' }}
                      >
                        {show ? <EyeOff size={14} /> : <Eye size={14} />}
                      </button>
                    )}
                  </div>
                  {field.hint && (
                    <p className="text-xs mt-1.5" style={{ color: 'rgba(255,255,255,0.3)' }}>
                      {field.hint}
                    </p>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      ))}

      {error && (
        <div
          className="flex items-center gap-2 p-4 rounded-xl text-sm"
          style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.2)', color: '#fca5a5' }}
        >
          <AlertCircle size={16} />
          {error}
        </div>
      )}

      <div className="flex items-center gap-4">
        <button
          type="submit"
          disabled={saving}
          className="flex items-center gap-2 px-5 py-2.5 rounded-lg text-sm font-bold transition-all"
          style={{
            background: saved ? 'rgba(16,185,129,0.2)' : 'rgba(16,185,129,0.15)',
            border: `1px solid ${saved ? 'rgba(16,185,129,0.5)' : 'rgba(16,185,129,0.3)'}`,
            color: '#6ee7b7',
          }}
        >
          {saving ? <Loader2 size={14} className="animate-spin" /> : saved ? <CheckCircle size={14} /> : <Save size={14} />}
          {saving ? 'Saving…' : saved ? 'Saved!' : 'Save Settings'}
        </button>
        {saved && (
          <p className="text-sm" style={{ color: 'rgba(255,255,255,0.4)' }}>
            Changes will apply on the next poll cycle.
          </p>
        )}
      </div>
    </form>
  );
}
