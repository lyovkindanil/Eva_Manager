// Длительность по-русски: «3 д 4 ч», «5 мин», «12 с».
export function duration(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d} д ${h} ч`;
  if (h > 0) return `${h} ч ${m} мин`;
  if (m > 0) return `${m} мин`;
  return `${Math.max(0, Math.floor(seconds))} с`;
}

export function dbName(dialect: string): string {
  return dialect === "postgres" ? "PostgreSQL" : "SQLite";
}
