// Hash routes: #/ (traffic), #/rules/<id>, #/setup, #/docs/<slug>, #/uninstall.
export type Page = 'traffic' | 'compose' | 'rules' | 'breakpoints' | 'setup' | 'docs' | 'uninstall';

function read(): { page: Page; sub: string } {
  const [, page = '', sub = ''] = location.hash.replace(/^#/, '').split('/');
  if (page === 'flow') return { page: 'traffic', sub: decodeURIComponent(sub) }; // #/flow/<id> opens a flow
  const p = (['compose', 'rules', 'breakpoints', 'setup', 'docs', 'uninstall'] as const).find((x) => x === page) ?? 'traffic';
  return { page: p, sub: decodeURIComponent(sub) };
}

class Route {
  current = $state(read());
  constructor() {
    addEventListener('hashchange', () => (this.current = read()));
  }
}

export const route = new Route();

export function href(page: Page, sub = ''): string {
  if (page === 'traffic') return sub ? `#/flow/${encodeURIComponent(sub)}` : '#/';
  return `#/${page}${sub ? '/' + encodeURIComponent(sub) : ''}`;
}
