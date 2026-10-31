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

export type Status = {
  version: string;
  database: { dialect: "sqlite" | "postgres"; schema: number };
  xray: XrayStatus;
  nodes: Node[];
};

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path, { headers: { Accept: "application/json" } });
  const body = await r.json().catch(() => null);
  if (!r.ok) {
    throw new Error(body?.error ?? `HTTP ${r.status}`);
  }
  return body as T;
}

export const api = {
  status: () => get<Status>("/api/status"),
  xrayLogs: () => get<{ lines: string[] }>("/api/xray/logs"),
};
