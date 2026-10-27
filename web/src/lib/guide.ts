import { has, t, tlist } from './i18n';

// The phone setup slideshow (Setup page and the phone's certificate page).
// Rows come from the locale as "label|value": "*" marks the row to tap, "~" is a
// skeleton row, and [on] [off] [>] [i] [check] in the value draw a control.
export interface Row {
  label: string;
  value: string;
  target: boolean;
  skeleton: boolean;
}
export interface Scene {
  title: string;
  caption: string;
  url: string;
  rows: Row[];
}

export function guideScenes(platform: 'ios' | 'android', ip: string, port: string): Scene[] {
  const fill = (s: string) => s.replaceAll('{ip}', ip || '192.168.1.20').replaceAll('{port}', port || '9090');
  const out: Scene[] = [];
  for (let i = 1; has(`setup.guide.${platform}.${i}.title`); i++) {
    const k = `setup.guide.${platform}.${i}`;
    out.push({
      title: fill(t(`${k}.title`)),
      caption: fill(t(`${k}.caption`)),
      url: has(`${k}.url`) ? t(`${k}.url`) : '',
      rows: tlist(`${k}.rows`).map((r) => {
        const target = r.startsWith('*');
        const [label, value = ''] = fill(target ? r.slice(1) : r).split('|');
        return { label, value, target, skeleton: label === '~' };
      }),
    });
  }
  return out;
}
