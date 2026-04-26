import { Routes, Route, NavLink, useLocation } from 'react-router-dom';
import { LayoutGrid, Settings as SettingsIcon, Film } from 'lucide-react';
import { Dashboard } from './pages/Dashboard';
import { ShowDetail } from './pages/ShowDetail';
import { Settings } from './pages/Settings';

function NavBar() {
  return (
    <header
      className="sticky top-0 z-40 px-6 py-3 flex items-center justify-between"
      style={{
        background: 'rgba(9,9,15,0.85)',
        backdropFilter: 'blur(12px)',
        borderBottom: '1px solid rgba(255,255,255,0.05)',
      }}
    >
      {/* Logo */}
      <div className="flex items-center gap-2.5">
        <div
          className="p-1.5 rounded-lg"
          style={{ background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.2)' }}
        >
          <Film size={16} style={{ color: 'var(--emerald)' }} />
        </div>
        <span className="font-extrabold text-white tracking-tight text-lg">
          Roll<span style={{ color: 'var(--emerald)' }}>arr</span>
        </span>
      </div>

      {/* Nav */}
      <nav className="flex items-center gap-1">
        {[
          { to: '/',         icon: <LayoutGrid size={14} />,      label: 'Dashboard' },
          { to: '/settings', icon: <SettingsIcon size={14} />,    label: 'Settings' },
        ].map(({ to, icon, label }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-all ${
                isActive ? 'active-nav' : ''
              }`
            }
            style={({ isActive }) => ({
              background: isActive ? 'rgba(16,185,129,0.12)' : 'transparent',
              color: isActive ? '#6ee7b7' : 'rgba(255,255,255,0.45)',
              border: isActive ? '1px solid rgba(16,185,129,0.2)' : '1px solid transparent',
            })}
          >
            {icon}
            {label}
          </NavLink>
        ))}
      </nav>
    </header>
  );
}

export default function App() {
  const location = useLocation();

  return (
    <div className="min-h-screen flex flex-col">
      <NavBar />
      <main className="flex-1 px-6 py-8 max-w-7xl mx-auto w-full">
        <Routes location={location}>
          <Route path="/"          element={<Dashboard />} />
          <Route path="/shows/:id" element={<ShowDetail />} />
          <Route path="/settings"  element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}
