// Control API client. Auth is the HttpOnly cookie set by /?token=… (same origin).

export interface FlowRow {
  id: string;
  method: string;
  scheme: string;
  host: string;
  path: string;
  url: string;
  status?: number;
  state: 'pending' | 'completed' | 'failed' | 'tunneled' | 'paused';
  content_type?: string;
  bytes_up: number;
  bytes_down: number;
  resp_size: number;
  total_ms: number;
  error_kind?: string;
  client_label?: string;
  source: string;
  matched_rules?: string[];
  tab?: string;
  gql_op?: string;
  gql_type?: string;
  device?: string;
  app?: string;
  created_at: string;
}

/** Full flow from GET /flows (list) or /flows/{id}. */
export interface Flow {
  id: string;
  source: string;
  client_addr: string;
  client_label?: string;
  method: string;
  scheme: string;
  host: string;
  port: number;
  path: string;
  query: string;
  url: string;
  status: number;
  resp_content_type: string;
  req_size: number;
  resp_size: number;
  bytes_up: number;
  bytes_down: number;
  protocol: string;
  state: FlowRow['state'];
  error_kind?: string;
  error_msg?: string;
  matched_rules?: string[];
  tags?: Record<string, string>;
  timing: Timing;
  req_content_type?: string;
  req_headers?: Header[];
  resp_headers?: Header[];
  created_at: string;
}

export interface Timing {
  dns_ms?: number;
  connect_ms?: number;
  tls_ms?: number;
  ttfb_ms?: number;
  total_ms: number;
  reused?: boolean;
}

export interface Header {
  name: string;
  value: string;
}

export interface BodyInfo {
  side: 'req' | 'resp';
  encoding: string;
  raw_size: number;
  decoded_size: number;
  truncated: boolean;
  on_disk: boolean;
}

/** GET /flows/{id}: flow plus body metadata. */
export interface FlowDetail extends Flow {
  bodies: { req?: BodyInfo; resp?: BodyInfo };
}

export interface Body {
  size: number;
  returned: number;
  truncated: boolean;
  content_type?: string;
  content_encoding?: string;
  encoding: 'utf8' | 'base64';
  data?: string;
}

export interface Status {
  running: boolean;
  /** "No caching" is on: clients can't use their caches. */
  no_caching?: boolean;
  /** Active throttle profile; absent when off. */
  throttle?: string;
  proxy_addr: string;
  api_addr: string;
  flows: number;
  active_rules: number;
  pending_breakpoints: number;
  recording: boolean;
  max_entries: number;
  data_dir: string;
  ca_fingerprint: string;
  version: string;
}

export interface SSLPolicy {
  mode: 'all' | 'allowlist' | 'off';
  allowlist: string[];
  denylist: string[];
  /** Hosts tunneled for this run: host → reason (pinning, mTLS). */
  learned_tunnels?: Record<string, string>;
}

export interface DiffChange {
  path: string;
  kind: 'added' | 'removed' | 'changed';
  a?: string;
  b?: string;
}

export interface DiffSide {
  line: DiffChange[] | null;
  headers: DiffChange[] | null;
  body: {
    mode: 'same' | 'json' | 'text' | 'binary' | 'empty';
    changes?: DiffChange[];
    lines?: { op: ' ' | '-' | '+' | '…'; text?: string }[];
    truncated?: boolean;
  };
}

export interface FlowDiff {
  a: { id: string; method: string; url: string; status?: number };
  b: { id: string; method: string; url: string; status?: number };
  same: boolean;
  request: DiffSide;
  response: DiffSide;
}

export interface Simulator {
  udid: string;
  name: string;
  runtime: string;
  state: string;
  booted: boolean;
  ca: 'installed' | 'missing' | 'unknown';
}

export interface InstallResult {
  udid: string;
  name: string;
  ok: boolean;
  error?: string;
}

export interface ProxySetting {
  enabled: boolean;
  server: string;
  port: number;
}

export interface SystemProxy {
  status: {
    services: { name: string; web: ProxySetting; secure: ProxySetting; bypass: string[] }[];
    enabled: boolean;
    managed: boolean;
    backup_at?: string;
    target: string;
  };
  on_commands: string[];
  off_commands?: string[];
}

export interface UninstallStep {
  id: 'system_proxy' | 'simulators' | 'android' | 'mac_trust' | 'devices' | 'data';
  status: 'pending' | 'clean' | 'manual' | 'unavailable';
  auto: boolean;
  details?: string[];
  commands?: string[];
}

export interface CAInfo {
  common_name: string;
  fingerprint_sha256: string;
  cert_path: string;
  trust: 'trusted' | 'not_trusted' | 'unknown';
  trust_command: string;
}

export interface Rule {
  id: string;
  name: string;
  type: 'map_local' | 'breakpoint' | 'rewrite' | 'map_remote';
  enabled: boolean;
  owner: string;
  expires_at?: string;
  match: { url: string; regex?: boolean; methods?: string[]; headers?: Record<string, string>; graphql_operation?: string };
  phase?: string;
  spec: {
    file?: string;
    dir?: string;
    body?: string;
    body_base64?: string;
    status?: number;
    headers?: Record<string, string>;
    mode?: 'full' | 'body_only';
    timeout?: string;
    on_timeout?: 'continue' | 'abort';
    to?: string;
    preserve_host_header?: boolean;
    steps?: RewriteStep[];
  };
  priority: number;
  created_at: string;
  hits: number;
}

export interface RewriteStep {
  type: string;
  name?: string;
  value?: string;
  pattern?: string;
  regex?: boolean;
  path?: string;
  status?: number;
}

export type RuleInput = Omit<Rule, 'id' | 'created_at' | 'hits'> & { ttl?: string };

export interface BPHeader {
  name: string;
  value: string;
}
export interface BPRequest {
  method: string;
  url: string;
  headers: BPHeader[];
  body: string;
  body_base64?: string;
}
export interface BPResponse {
  status: number;
  headers: BPHeader[];
  body: string;
  body_base64?: string;
  encoding?: string;
}
/** The response a Map Local rule serves (Rules editor). */
export interface RuleContent {
  source: 'file' | 'inline' | 'base64' | 'dir';
  path?: string;
  content: string;
  size: number;
  editable: boolean;
  reason?: string;
}

export interface Paused {
  id: string;
  flow_id: string;
  phase: 'request' | 'response';
  rule_id: string;
  rule_name: string;
  owner: string;
  source: string;
  client?: string;
  created_at: string;
  expires_at: string;
  on_timeout: 'continue' | 'abort';
  request: BPRequest;
  response?: BPResponse;
  truncated?: boolean;
}
export interface Resolution {
  action: 'continue' | 'modify' | 'abort';
  request?: BPRequest;
  response?: BPResponse;
}

export interface DeviceSetup {
  proxy_addr: string;
  port: string;
  lan_ips: string[];
  reachable: boolean;
  page_url?: string;
  firewall: 'on' | 'off' | 'unknown';
  warnings?: string[];
  magic_url: string;
  /** Network access comes from the listen address (--proxy-listen), not the Setup switch. */
  lan_fixed?: boolean;
}
export interface WSMessage {
  seq: number;
  dir: 'up' | 'down';
  at: string;
  opcode: string;
  size: number;
  text?: string;
  base64?: string;
  truncated?: boolean;
}
export interface ThrottleProfile {
  name: string;
  down_kbps: number;
  up_kbps: number;
  latency_ms: number;
}
export interface ThrottleInfo extends ThrottleProfile {
  enabled: boolean;
  hosts?: string[];
  profiles: ThrottleProfile[];
}
export interface ClientDevice {
  ip: string;
  name: string;
  guess: string;
  custom?: boolean;
  user_agent?: string;
  local: boolean;
  first_seen: string;
  last_seen: string;
  requests: number;
  tls: '' | 'ok' | 'rejected' | 'tunneled';
  rejected_host?: string;
}

export interface AndroidDevice {
  serial: string;
  state: string;
  model?: string;
  emulator: boolean;
  api_level?: number;
  proxy?: string;
  ours: boolean;
  user_ca_pushed: boolean;
  system_ca: boolean;
}
export interface AndroidResult {
  device: AndroidDevice;
  mode: string;
  proxy: string;
  ca: string;
  done: string[];
  manual?: string[];
  warnings?: string[];
}

export interface ComposeReq {
  method: string;
  url: string;
  headers: Header[];
  body?: string;
}
export interface SendSummary {
  results: { flow_id: string; status?: number; ms: number; aborted?: boolean; error?: string }[];
  by_status: Record<string, number>;
  avg_ms: number;
}

export interface ChromeTab {
  id: string;
  title: string;
  url: string;
  watching: boolean;
  requests: number;
}
export interface ChromeStatus {
  connected: boolean;
  launched: boolean;
  endpoint?: string;
  browser?: string;
  tabs: ChromeTab[];
  error?: string;
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string, public body?: any) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let code = 'http_error';
    let msg = res.statusText;
    let body: any;
    try {
      body = await res.json();
      code = body.error?.code ?? code;
      msg = body.error?.message ?? msg;
    } catch {
      /* not JSON */
    }
    throw new APIError(res.status, code, msg, body);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

/** Downloads a POST response as a file (exports of a selection). */
async function download(path: string, body: unknown, fallbackName: string): Promise<void> {
  const res = await fetch(`/api/v1${path}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new APIError(res.status, 'export_failed', res.statusText);
  const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? fallbackName;
  const url = URL.createObjectURL(await res.blob());
  const a = Object.assign(document.createElement('a'), { href: url, download: name });
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Uploads a HAR or session file with progress (0..1). */
function upload(file: File, onProgress: (p: number) => void): Promise<{ session: string; imported: number; skipped: number }> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open('POST', '/api/v1/import');
    x.withCredentials = true;
    x.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    x.onload = () => {
      try {
        const body = JSON.parse(x.responseText);
        x.status < 300 ? resolve(body) : reject(new APIError(x.status, body.error?.code ?? 'import_failed', body.error?.message ?? x.statusText));
      } catch (e) {
        reject(e);
      }
    };
    x.onerror = () => reject(new Error('upload failed'));
    x.send(file);
  });
}

export const api = {
  exportFile: (kind: 'har' | 'session', ids: string[] | null, redact: boolean) =>
    ids ? download(`/export/${kind}`, { ids, redact }, `proxypilot.${kind === 'har' ? 'har' : 'proxypilot'}`)
      : download(`/export/${kind}`, { redact }, `proxypilot.${kind === 'har' ? 'har' : 'proxypilot'}`),
  importFile: upload,
  status: () => call<Status>('GET', '/status'),
  flows: (limit = 500) => call<{ flows: Flow[] }>('GET', `/flows?limit=${limit}`),
  flow: (id: string, redact = false) =>
    call<FlowDetail>('GET', `/flows/${encodeURIComponent(id)}${redact ? '?redact=true' : ''}`),
  body: (id: string, side: 'req' | 'resp', max: number, redact: boolean) =>
    call<Body>('GET', `/flows/${encodeURIComponent(id)}/body/${side}?max=${max}${redact ? '&redact=true' : ''}`),
  bodyURL: (id: string, side: 'req' | 'resp') => `/api/v1/flows/${encodeURIComponent(id)}/body/${side}?raw=true`,
  clear: () => call<unknown>('DELETE', '/flows'),
  summaryAll: () => call<{ bytes_up: number; bytes_down: number }>('GET', '/summary'),
  messages: (id: string, after: number, limit: number) =>
    call<{ messages: WSMessage[]; next: number; total: number }>('GET', `/flows/${encodeURIComponent(id)}/messages?after=${after}&limit=${limit}`),
  throttle: () => call<ThrottleInfo>('GET', '/throttle'),
  setThrottle: (s: { enabled: boolean; name?: string; hosts?: string[] }) => call<ThrottleInfo>('PUT', '/throttle', s),
  setNoCaching: (enabled: boolean) => call<{ enabled: boolean }>('PUT', '/no-caching', { enabled }),
  setRecording: (recording: boolean) => call<{ recording: boolean }>('PUT', '/recording', { recording }),
  simulators: () => call<{ simulators: Simulator[]; ca_path: string; install_command: string }>('GET', '/devices/simulators'),
  installCA: (udid?: string) =>
    call<{ results: InstallResult[] }>('POST', udid ? `/devices/simulators/${encodeURIComponent(udid)}/install-ca` : '/devices/simulators/install-ca'),
  systemProxy: () => call<SystemProxy>('GET', '/system-proxy'),
  setSystemProxy: (enabled: boolean) => call<SystemProxy>('PUT', '/system-proxy', { enabled }),
  uninstallPlan: () => call<{ steps: UninstallStep[] }>('GET', '/uninstall'),
  uninstall: () => call<{ results: { id: string; ok: boolean; done?: string[]; error?: string }[]; steps: UninstallStep[] }>('POST', '/uninstall'),
  ca: () => call<CAInfo>('GET', '/ca'),
  android: () =>
    call<{ adb: string; error?: string; devices: AndroidDevice[]; notes: { network_security_config: string; flutter: string } }>('GET', '/devices/android'),
  androidSetup: (serial: string, mode: string, ca: string) => call<AndroidResult>('POST', '/devices/android/setup', { serial, mode, ca }),
  androidRemove: (serial: string) => call<AndroidResult>('POST', '/devices/android/remove', { serial }),
  deviceSetup: () => call<DeviceSetup>('GET', '/devices/setup'),
  clients: () => call<{ clients: ClientDevice[] }>('GET', '/devices/clients'),
  renameClient: (ip: string, name: string) => call<ClientDevice>('PUT', `/devices/clients/${encodeURIComponent(ip)}`, { name }),
  setLAN: (enabled: boolean) => call<DeviceSetup>('PUT', '/devices/lan', { enabled }),
  qrURL: (text: string) => `/api/v1/devices/qr.png?text=${encodeURIComponent(text)}`,
  rules: () => call<{ rules: Rule[] }>('GET', '/rules'),
  ruleContent: (id: string) => call<RuleContent>('GET', `/rules/${encodeURIComponent(id)}/content`),
  setRuleContent: (id: string, content: string) => call<RuleContent>('PUT', `/rules/${encodeURIComponent(id)}/content`, { content }),
  curl: (id: string) => call<{ curl: string; binary_body: boolean }>('GET', `/flows/${encodeURIComponent(id)}/curl`),
  chrome: () => call<ChromeStatus>('GET', '/chrome'),
  chromeLaunch: (url: string) => call<ChromeStatus>('POST', '/chrome/launch', { url }),
  chromeAttach: (addr: string) => call<ChromeStatus>('POST', '/chrome/attach', { addr }),
  chromeDetach: () => call<ChromeStatus>('POST', '/chrome/detach', {}),
  chromeWatch: (id: string, watch: boolean) => call<ChromeTab>('PUT', `/chrome/tabs/${encodeURIComponent(id)}`, { watch }),
  editableRequest: (id: string) => call<ComposeReq>('GET', `/flows/${encodeURIComponent(id)}/request`),
  repeat: (id: string, opts: Record<string, unknown> = {}) => call<SendSummary>('POST', `/flows/${encodeURIComponent(id)}/repeat`, opts),
  compose: (req: ComposeReq & { count?: number; concurrency?: number; bypass_rules?: boolean }) => call<SendSummary>('POST', '/compose', req),
  parseCurl: (curl: string) => call<ComposeReq>('POST', '/curl/parse', { curl }),
  breakpoints: () => call<{ breakpoints: Paused[] }>('GET', '/breakpoints'),
  resume: (id: string, r: Resolution) => call<unknown>('POST', `/breakpoints/${encodeURIComponent(id)}/resume`, r),
  extend: (id: string, seconds = 30) => call<Paused>('POST', `/breakpoints/${encodeURIComponent(id)}/extend`, { seconds }),
  takeOver: (id: string) => call<Paused>('POST', `/breakpoints/${encodeURIComponent(id)}/take-over`, {}),
  addRule: (r: RuleInput) => call<Rule>('POST', '/rules', r),
  updateRule: (id: string, r: RuleInput) => call<Rule>('PUT', `/rules/${encodeURIComponent(id)}`, r),
  deleteRule: (id: string) => call<unknown>('DELETE', `/rules/${encodeURIComponent(id)}`),
  toggleRule: (id: string, enabled: boolean) => call<Rule>('POST', `/rules/${encodeURIComponent(id)}/toggle`, { enabled }),
  clearRules: (owner: string) => call<{ deleted: number }>('DELETE', `/rules?owner=${encodeURIComponent(owner)}`),
  mapLocalFromFlow: (id: string) => call<{ rule: Rule; file: string }>('POST', `/flows/${encodeURIComponent(id)}/map-local`, {}),
  sslPolicy: () => call<SSLPolicy>('GET', '/ssl-policy'),
  setSSLPolicy: (p: SSLPolicy) =>
    call<SSLPolicy>('PUT', '/ssl-policy', { mode: p.mode, allowlist: p.allowlist, denylist: p.denylist }),
  diff: (a: string, b: string) => call<FlowDiff>('GET', `/diff?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}`),
  forgetTunnel: (host: string) => call<SSLPolicy>('DELETE', `/ssl-policy/learned/${encodeURIComponent(host)}`),
};

/** Same shape the stream sends, from a REST flow. */
export function toRow(f: Flow): FlowRow {
  return {
    id: f.id, method: f.method, scheme: f.scheme, host: f.host, path: f.path, url: f.url,
    status: f.status || undefined, state: f.state, content_type: f.resp_content_type,
    bytes_up: f.bytes_up, bytes_down: f.bytes_down, resp_size: f.resp_size, total_ms: f.timing?.total_ms ?? 0,
    error_kind: f.error_kind, client_label: f.client_label, source: f.source, matched_rules: f.matched_rules, tab: f.tags?.tab_title,
    gql_op: f.tags?.gql_op, gql_type: f.tags?.gql_type, device: f.tags?.device, app: f.tags?.app,
    created_at: f.created_at,
  };
}
