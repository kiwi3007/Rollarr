import { useState, useEffect } from 'react';
import { Routes, Route, NavLink } from 'react-router-dom';
import { LayoutGrid, Settings as SettingsIcon, Film, Sun, Moon, Flag } from 'lucide-react';
import { Dashboard } from './pages/Dashboard';
import { ShowDetail } from './pages/ShowDetail';
import { FlagsPage } from './pages/FlagsPage';
import { Settings } from './pages/Settings';
import { BackdropContext } from './context/BackdropContext';

function bgFilter(isDark: boolean) {
  return isDark
    ? 'blur(32px) brightness(0.28) saturate(1.5) contrast(1.1)'
    : 'blur(32px) brightness(0.82) saturate(0.75) contrast(0.95)';
}

function BackgroundArtwork({ isDark, url }: { isDark: boolean; url: string | null }) {
  return (
    <>
      <div className="bg-fallback" />
      {url && (
        <div
          className="bg-layer"
          style={{ backgroundImage: `url(${url})`, filter: bgFilter(isDark) }}
        />
      )}
      <div className="bg-vignette" />
    </>
  );
}

export default function App() {
  const [isDark, setIsDark] = useState(true);
  const [backdropUrl, setBackdropUrl] = useState<string | null>(null);

  useEffect(() => {
    document.documentElement.classList.toggle('light', !isDark);
  }, [isDark]);

  const navItems = [
    { to: '/',         icon: <LayoutGrid size={18} />,   label: 'Dashboard' },
    { to: '/flags',    icon: <Flag size={18} />,         label: 'Flags'     },
    { to: '/settings', icon: <SettingsIcon size={18} />, label: 'Settings'  },
  ];

  return (
    <BackdropContext.Provider value={{ setBackdrop: setBackdropUrl }}>
      <BackgroundArtwork isDark={isDark} url={backdropUrl} />

      <aside className="sidebar">
        <div className="sidebar-logo" title="Rollarr">
          <Film size={18} style={{ color: 'var(--color-accent-orange)' }} />
        </div>

        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 4, width: '100%', padding: '0 8px' }}>
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
            >
              {item.icon}
              <span className="nav-tooltip">{item.label}</span>
            </NavLink>
          ))}
        </div>

        <div style={{ padding: '0 8px', width: '100%' }}>
          <hr className="glass-divider" style={{ marginBottom: 8 }} />
          <button
            className="nav-item"
            onClick={() => setIsDark((d) => !d)}
            title={isDark ? 'Light mode' : 'Dark mode'}
            style={{ width: '100%', cursor: 'pointer', background: 'none' }}
          >
            {isDark ? <Sun size={18} /> : <Moon size={18} />}
            <span className="nav-tooltip">{isDark ? 'Light mode' : 'Dark mode'}</span>
          </button>
        </div>
      </aside>

      <main className="main-content">
        <Routes>
          <Route path="/"                  element={<Dashboard />} />
          <Route path="/shows/:tvdbId"     element={<ShowDetail />} />
          <Route path="/flags"             element={<FlagsPage />} />
          <Route path="/settings"          element={<Settings />} />
        </Routes>
      </main>
    </BackdropContext.Provider>
  );
}
