import { describe, expect, it } from 'vitest';
import { GROUPS, addDays, grp, isoDay } from './format';

describe('biçimlendirme', () => {
  it('kan grubu yazımı', () => {
    expect(grp('AB', false)).toBe('AB-');
    expect(grp('O', true)).toBe('O+');
    expect(GROUPS).toHaveLength(8);
  });
  it('gün hesapları', () => {
    const d = new Date('2026-10-30T10:00:00Z');
    expect(isoDay(d)).toBe('2026-10-30');
    expect(isoDay(addDays(3, d))).toBe('2026-11-02');
  });
});
