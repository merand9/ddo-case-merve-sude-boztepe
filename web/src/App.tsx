import { lazy, Suspense, type ReactNode } from 'react';
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth';
import Layout from './components/Layout';
import { Loading, ToastProvider } from './components/ui';
import type { Role } from './format';
import { HOME } from './routes';
import Login from './pages/Login';

const Live = lazy(() => import('./pages/Live'));
const Donors = lazy(() => import('./pages/Donors'));
const DonorDetail = lazy(() => import('./pages/DonorDetail'));
const Appointments = lazy(() => import('./pages/Appointments'));
const Lab = lazy(() => import('./pages/Lab'));
const Stock = lazy(() => import('./pages/Stock'));
const Requests = lazy(() => import('./pages/Requests'));
const HospitalProducts = lazy(() => import('./pages/HospitalProducts'));
const Forecast = lazy(() => import('./pages/Forecast'));
const Trace = lazy(() => import('./pages/Trace'));
const Reports = lazy(() => import('./pages/Reports'));
const Admin = lazy(() => import('./pages/Admin'));
const Me = lazy(() => import('./pages/Me'));

// Guard: oturum yoksa girişe, rol yetkisizse rolün ana ekranına yönlendirir.
function Guard({ roles, children }: { roles: Role[]; children: ReactNode }) {
  const { user } = useAuth();
  const loc = useLocation();
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname }} />;
  if (!roles.includes(user.role)) return <Navigate to={HOME[user.role]} replace />;
  return <Suspense fallback={<Loading />}>{children}</Suspense>;
}

function Home() {
  const { user } = useAuth();
  return <Navigate to={user ? HOME[user.role] : '/login'} replace />;
}

const STAFF: Role[] = ['nurse', 'lab', 'bank', 'admin'];

export default function App() {
  return (
    <AuthProvider>
      <ToastProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route element={<Layout />}>
              <Route index element={<Home />} />
              <Route path="live" element={<Guard roles={STAFF}><Live /></Guard>} />
              <Route path="donors" element={<Guard roles={['nurse', 'bank', 'admin']}><Donors /></Guard>} />
              <Route path="donors/:id" element={<Guard roles={['nurse', 'admin']}><DonorDetail /></Guard>} />
              <Route path="appointments" element={<Guard roles={['nurse', 'admin']}><Appointments /></Guard>} />
              <Route path="lab" element={<Guard roles={['lab', 'admin']}><Lab /></Guard>} />
              <Route path="stock" element={<Guard roles={['lab', 'bank', 'admin']}><Stock /></Guard>} />
              <Route path="requests" element={<Guard roles={['bank', 'admin', 'hospital']}><Requests /></Guard>} />
              <Route path="hospital-products" element={<Guard roles={['hospital']}><HospitalProducts /></Guard>} />
              <Route path="forecast" element={<Guard roles={['bank', 'admin']}><Forecast /></Guard>} />
              <Route path="trace" element={<Guard roles={['bank', 'admin']}><Trace /></Guard>} />
              <Route path="reports" element={<Guard roles={['bank', 'admin']}><Reports /></Guard>} />
              <Route path="admin" element={<Guard roles={['admin']}><Admin /></Guard>} />
              <Route path="me" element={<Guard roles={['donor']}><Me /></Guard>} />
              <Route path="*" element={<Home />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </ToastProvider>
    </AuthProvider>
  );
}
