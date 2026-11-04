// Ответы API панели (internal/server). Вход — cookie сессии; изменяющие
// запросы несут заголовок X-Eva-Request (защита от CSRF).

export type Node = {
  id: string;
  name: string;
  address: string;
  is_local: boolean;
  created_at: string;
};

export type XrayStatus = {
  installed: boolean;
  version?: string;
  running: boolean;
  pid?: number;
  since?: string;
  last_error?: string;
  problem?: string;
  api: boolean;
  uptime_s?: number;
};

// Интерфейс AmneziaWG: процесс awg-run на сервере.
export type AWGStatus = {
  id: string;
  name: string;
  iface: string;
  running: boolean;
  problem?: string;
};

export type Status = {
  version: string;
  database: { dialect: "sqlite" | "postgres"; schema: number };
  xray: XrayStatus;
  awg: AWGStatus[];
  nodes: Node[];
};

export type UserStatus = "active" | "disabled" | "expired" | "limited";

export type User = {
  id: string;
  name: string;
  uuid: string;
  sub_token: string;
  traffic_limit: number;
  traffic_used: number;
  expires_at: string; // "0001-01-01T00:00:00Z" — бессрочно
  status: UserStatus;
  note: string;
  created_at: string;
};

export type Access = { kind: "vless" | "awg"; name: string; link: string; awg?: string };

export type UserView = User & { access: Access[]; sub_url: string };

export type TrafficDay = { day: string; uplink: number; downlink: number };

export type Inbound = {
  id: string;
  name: string;
  protocol: string;
  port: number;
  host: string;
  enabled: boolean;
  dest: string;
  public_key: string;
  short_id: string;
  encryption: boolean;
  fingerprint: string;
};

export type AWG = {
  id: string;
  name: string;
  iface: string;
  port: number;
  host: string;
  enabled: boolean;
  subnet: string;
  mtu: number;
  dns: string;
  public_key: string;
  running: boolean;
  problem?: string;
};

// Изменение инбаунда: запись в базе есть всегда, applied — дошло ли до ядра.
export type ApplyResult<T> = { item?: T; applied: boolean; error?: string };

export type Settings = { sub_url: string; sub_url_from_env: boolean };

export type Me = { username: string; via: "session" | "token" };

// Unauthorized — нужен вход.
export class Unauthorized extends Error {}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method !== "GET") headers["X-Eva-Request"] = "1";
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const r = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await r.json().catch(() => null);
  if (r.status === 401) {
    // Сессия кончилась посреди работы — App покажет вход.
    if (path !== "/api/me" && path !== "/api/login") window.dispatchEvent(new Event("eva-unauthorized"));
    throw new Unauthorized(data?.error ?? "нужен вход");
  }
  if (!r.ok) throw new Error(data?.error ?? `HTTP ${r.status}`);
  return data as T;
}

const get = <T>(p: string) => call<T>("GET", p);

export const api = {
  status: () => get<Status>("/api/status"),
  xrayLogs: () => get<{ lines: string[] }>("/api/xray/logs"),

  setupNeeded: () => get<{ needed: boolean }>("/api/setup"),
  setup: (username: string, password: string, token: string) =>
    call<Me>("POST", "/api/setup", { username, password, token }),
  login: (username: string, password: string) => call<Me>("POST", "/api/login", { username, password }),
  logout: () => call("POST", "/api/logout"),
  me: () => get<Me>("/api/me"),

  users: () => get<User[]>("/api/users"),
  user: (id: string) => get<UserView>(`/api/users/${id}`),
  createUser: (u: { name: string; note?: string; traffic_limit?: number; expires_at?: string }) =>
    call<UserView>("POST", "/api/users", u),
  patchUser: (id: string, p: { note?: string; traffic_limit?: number; expires_at?: string; enabled?: boolean }) =>
    call<UserView>("PATCH", `/api/users/${id}`, p),
  deleteUser: (id: string) => call("DELETE", `/api/users/${id}`),
  resetTraffic: (id: string) => call<UserView>("POST", `/api/users/${id}/reset-traffic`),
  rotateSub: (id: string) => call<UserView>("POST", `/api/users/${id}/rotate-sub`),
  traffic: (id: string, days = 30) => get<TrafficDay[]>(`/api/users/${id}/traffic?days=${days}`),
  awgConfURL: (id: string, awg: string) => `/api/users/${id}/awg/${awg}`,

  inbounds: () => get<Inbound[]>("/api/inbounds"),
  createInbound: (p: { name: string; host: string; port?: number; dest?: string; encryption?: boolean }) =>
    call<ApplyResult<Inbound>>("POST", "/api/inbounds", p),
  patchInbound: (id: string, enabled: boolean) => call<ApplyResult<Inbound>>("PATCH", `/api/inbounds/${id}`, { enabled }),
  deleteInbound: (id: string) => call<ApplyResult<never>>("DELETE", `/api/inbounds/${id}`),

  awg: () => get<AWG[]>("/api/awg"),
  createAWG: (p: { name?: string; host: string; port?: number; subnet?: string; dns?: string }) =>
    call<AWG>("POST", "/api/awg", p),
  patchAWG: (id: string, enabled: boolean) => call<AWG>("PATCH", `/api/awg/${id}`, { enabled }),
  deleteAWG: (id: string) => call("DELETE", `/api/awg/${id}`),

  settings: () => get<Settings>("/api/settings"),
  putSettings: (s: { sub_url: string }) => call<Settings>("PUT", "/api/settings", s),
};
