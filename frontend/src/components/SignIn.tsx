import { useState } from 'react';
import { KeyRound, Loader2 } from 'lucide-react';
import { checkToken } from '../api/client';
import { setToken } from '../auth';

/** Full-screen prompt shown when the API rejects (or lacks) a token. */
export function SignIn() {
  const [value, setValue] = useState('');
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const token = value.trim();
    if (!token) return;
    setChecking(true);
    setError(null);
    try {
      if (await checkToken(token)) {
        setToken(token);
        // Reload so every page and the live-update stream pick up the token.
        window.location.reload();
        return;
      }
      setError('That token was not accepted.');
    } catch {
      setError('Could not reach Rollarr.');
    }
    setChecking(false);
  }

  return (
    <div style={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', padding: 16, zIndex: 50 }}>
      <form onSubmit={handleSubmit} className="glass-panel" style={{ width: '100%', maxWidth: 380, padding: 24, display: 'grid', gap: 14 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontWeight: 600 }}>
          <KeyRound size={18} style={{ color: 'var(--color-accent-orange)' }} />
          Sign in to Rollarr
        </div>
        <p style={{ margin: 0, fontSize: '0.85rem', color: 'var(--color-text-muted)' }}>
          Enter the API token (ROLLARR_API_TOKEN). It is stored in this browser only.
        </p>
        <input
          className="glass-input"
          type="password"
          autoFocus
          autoComplete="current-password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="API token"
        />
        {error && <div style={{ fontSize: '0.85rem', color: 'var(--color-accent-red, #ef4444)' }}>{error}</div>}
        <button
          type="submit"
          disabled={checking || !value.trim()}
          style={{
            display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            height: 36, borderRadius: 'var(--radius-pill)', border: 'none',
            cursor: checking ? 'not-allowed' : 'pointer',
            background: 'var(--color-accent-orange)', color: '#fff', fontWeight: 600, fontSize: '0.875rem',
            opacity: checking || !value.trim() ? 0.7 : 1,
          }}
        >
          {checking ? <><Loader2 size={14} style={{ animation: 'spin 0.8s linear infinite' }} /> Checking…</> : 'Sign in'}
        </button>
      </form>
    </div>
  );
}
