// Ответы API панели (internal/server).

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

// Токен администратора (eva-manager token). До входа по паролю (этап 3)
// интерфейс хранит его в localStorage этого браузера.
const tokenKey = "eva-token";
export const token = {
  get: () => localStorage.getItem(tokenKey) ?? "",
  set: (t: string) => localStorage.setItem(tokenKey, t.trim()),
  clear: () => localStorage.removeItem(tokenKey),
};

// Unauthorized — нужен (другой) токен.
export class Unauthorized extends Error {}

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path, {
    headers: { Accept: "application/json", Authorization: `Bearer ${token.get()}` },
  });
  const body = await r.json().catch(() => null);
  if (r.status === 401) {
    throw new Unauthorized(body?.error ?? "нужен токен");
  }
  if (!r.ok) {
    throw new Error(body?.error ?? `HTTP ${r.status}`);
  }
  return body as T;
}

export const api = {
  status: () => get<Status>("/api/status"),
  xrayLogs: () => get<{ lines: string[] }>("/api/xray/logs"),
};
