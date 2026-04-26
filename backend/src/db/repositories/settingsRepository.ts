import db from '../database';
import type { SettingRow, SettingsMap } from '@rollarr/shared';

export const settingsRepository = {
  getAll(): SettingsMap {
    const rows = db.prepare(`SELECT key, value FROM settings`).all() as SettingRow[];
    return Object.fromEntries(rows.map((r) => [r.key, r.value]));
  },

  get(key: string): string | undefined {
    const row = db.prepare(`SELECT value FROM settings WHERE key = ?`).get(key) as
      | { value: string }
      | undefined;
    return row?.value;
  },

  getNumber(key: string, fallback: number): number {
    const val = this.get(key);
    const n = val !== undefined ? parseInt(val, 10) : NaN;
    return isNaN(n) ? fallback : n;
  },

  set(key: string, value: string): void {
    db.prepare(
      `INSERT INTO settings (key, value) VALUES (?, ?)
       ON CONFLICT(key) DO UPDATE SET value=excluded.value`
    ).run(key, value);
  },

  setMany(map: SettingsMap): void {
    const upsert = db.prepare(
      `INSERT INTO settings (key, value) VALUES (?, ?)
       ON CONFLICT(key) DO UPDATE SET value=excluded.value`
    );
    db.transaction(() => {
      for (const [key, value] of Object.entries(map)) {
        upsert.run(key, value);
      }
    })();
  },
};
