import { Navigate, NavLink, Outlet } from 'react-router-dom';
import { useAuth } from '../auth';
import { ROLE_TR } from '../format';
import { NAV } from '../routes';

export default function Layout() {
  const { user, logout } = useAuth();
  if (!user) return <Navigate to="/login" replace />;
  const items = NAV.filter((n) => n.roles.includes(user.role));
  return (
    <>
      <header className="top">
        <div className="top-inner">
          <NavLink className="logo" to="/">
            <svg viewBox="0 0 32 32" aria-hidden="true"><path d="M16 3C16 3 6 14 6 20a10 10 0 0 0 20 0C26 14 16 3 16 3z" fill="#b4232c" /></svg>
            DamarBağış
          </NavLink>
          <nav className="nav" aria-label="Ana menü">
            {items.map((n) => <NavLink key={n.path} to={n.path}>{n.title}</NavLink>)}
          </nav>
          <div className="who">
            <span className="live-dot" title="Sistem canlı" />
            <span className="name">{user.full_name}</span>
            <span className="badge">{ROLE_TR[user.role]}</span>
            <button className="small" onClick={logout}>Çıkış</button>
          </div>
        </div>
      </header>
      <main><Outlet /></main>
    </>
  );
}
