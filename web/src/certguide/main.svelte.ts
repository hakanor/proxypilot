import { mount } from 'svelte';
import CertGuide from './CertGuide.svelte';

// Mounted by the certificate page (internal/certpage/page.html) into #guide;
// the page's iOS / Android switch calls window.ppGuide.setPlatform.
const el = document.getElementById('guide');
if (el) {
  const props = $state({
    platform: (el.dataset.platform === 'android' ? 'android' : 'ios') as 'ios' | 'android',
    ip: el.dataset.ip ?? '',
    port: el.dataset.port ?? '9090',
  });
  mount(CertGuide, { target: el, props });
  (window as unknown as { ppGuide: unknown }).ppGuide = {
    setPlatform(p: string) {
      if (p === 'ios' || p === 'android') props.platform = p;
    },
  };
}
