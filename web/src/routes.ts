import type { Role } from './format';

export interface NavItem { path: string; title: string; roles: Role[] }

// Menü ve rol erişimi. Sunucu her uç noktada rolü ayrıca denetler; bu liste yalnızca arayüz içindir.
export const NAV: NavItem[] = [
  { path: '/live', title: 'Canlı pano', roles: ['nurse', 'lab', 'bank', 'admin'] },
  { path: '/donors', title: 'Bağışçılar', roles: ['nurse', 'bank', 'admin'] },
  { path: '/appointments', title: 'Randevular', roles: ['nurse', 'admin'] },
  { path: '/lab', title: 'Laboratuvar', roles: ['lab', 'admin'] },
  { path: '/stock', title: 'Stok', roles: ['lab', 'bank', 'admin'] },
  { path: '/requests', title: 'Talepler', roles: ['bank', 'admin', 'hospital'] },
  { path: '/hospital-products', title: 'Ürünlerimiz', roles: ['hospital'] },
  { path: '/forecast', title: 'Tahmin ve çağrı', roles: ['bank', 'admin'] },
  { path: '/trace', title: 'Geri izleme', roles: ['bank', 'admin'] },
  { path: '/reports', title: 'Raporlar', roles: ['bank', 'admin'] },
  { path: '/admin', title: 'Yönetim', roles: ['admin'] },
  { path: '/me', title: 'Bağışlarım', roles: ['donor'] },
];

export const HOME: Record<Role, string> = {
  donor: '/me', nurse: '/donors', lab: '/lab', bank: '/live', hospital: '/requests', admin: '/live',
};
