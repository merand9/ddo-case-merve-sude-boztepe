// Ortak sabitler ve biçimlendirme yardımcıları.

export type Role = 'donor' | 'nurse' | 'lab' | 'bank' | 'hospital' | 'admin';

export const ROLE_TR: Record<Role, string> = {
  donor: 'Bağışçı', nurse: 'Hemşire', lab: 'Laboratuvar', bank: 'Kan bankası', hospital: 'Hastane', admin: 'Yönetici',
};

export const STATUS_TR: Record<string, string> = {
  pending_test: 'Test bekliyor', available: 'Stokta', reserved: 'Rezerve', issued: 'Dağıtıldı', transfused: 'Transfüze',
  quarantined: 'Karantina', discarded: 'İmha', expired: 'SKT doldu', split: 'Bölündü',
  open: 'Açık', partial: 'Kısmi', cancelled: 'İptal', active: 'Aktif', released: 'Serbest', preempted: 'Devredildi',
  booked: 'Randevulu', completed: 'Tamamlandı', no_show: 'Gelmedi', pending: 'Bekliyor', approved: 'Onaylandı', rejected: 'Reddedildi',
  positive: 'Pozitif', negative: 'Negatif',
};

export const COMP_TR: Record<string, string> = { RBC: 'Eritrosit', PLT: 'Trombosit', FFP: 'Plazma' };
export const COMPONENTS = ['RBC', 'PLT', 'FFP'] as const;
export const GROUPS = ['O-', 'O+', 'A-', 'A+', 'B-', 'B+', 'AB-', 'AB+'] as const;
export const DONATION_TR: Record<string, string> = { whole_blood: 'Tam kan', platelet_apheresis: 'Trombosit aferezi' };

export const DEFERRAL_OPTIONS: [string, string][] = [
  ['travel_malaria', 'Sıtma bölgesine seyahat (365 g)'], ['tattoo_piercing', 'Dövme / piercing (180 g)'],
  ['surgery_major', 'Büyük cerrahi (180 g)'], ['antibiotic', 'Antibiyotik (14 g)'], ['isotretinoin', 'İzotretinoin (30 g)'],
  ['dental', 'Diş tedavisi (7 g)'], ['aspirin', 'Aspirin (yalnız trombosit, 5 g)'], ['iv_drug_use', 'Damar içi madde (kalıcı)'],
];

export const DEMO_ACCOUNTS: [string, string][] = [
  ['admin@damarbagis.local', 'Yönetici'], ['hemsire@damarbagis.local', 'Hemşire'], ['lab@damarbagis.local', 'Laboratuvar uzmanı'],
  ['lab2@damarbagis.local', 'Laboratuvar (2. onay)'], ['bankasorumlusu@damarbagis.local', 'Kan bankası sorumlusu'],
  ['hastane@damarbagis.local', 'Hastane'], ['bagisci@damarbagis.local', 'Bağışçı'],
];

export const fmtDate = (s?: string | null) => (s ? new Date(s).toLocaleDateString('tr-TR') : '—');
export const fmtDT = (s?: string | null) =>
  s ? new Date(s).toLocaleString('tr-TR', { dateStyle: 'short', timeStyle: 'short' }) : '—';
export const fmtTime = (s?: string | null) =>
  s ? new Date(s).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '';
export const grp = (abo: string, rh: boolean) => `${abo}${rh ? '+' : '-'}`;
export const isoDay = (d = new Date()) => d.toISOString().slice(0, 10);
export const addDays = (n: number, d = new Date()) => { const x = new Date(d); x.setDate(x.getDate() + n); return x; };
