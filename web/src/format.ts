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

const units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];

// Байты: «1,5 ГБ».
export function bytes(n: number): string {
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toLocaleString("ru-RU", { maximumFractionDigits: v < 10 && i > 0 ? 1 : 0 })} ${units[i]}`;
}

export const GB = 1024 ** 3;

// Срок: нулевое время Go — бессрочно.
export function isForever(t: string): boolean {
  return !t || t.startsWith("0001-");
}

export function date(t: string): string {
  if (isForever(t)) return "бессрочно";
  return new Date(t).toLocaleDateString("ru-RU", { day: "numeric", month: "short", year: "numeric" });
}

// Сколько дней осталось (отрицательное — истёк).
export function daysLeft(t: string): number | null {
  if (isForever(t)) return null;
  return Math.ceil((new Date(t).getTime() - Date.now()) / 86400000);
}

export const statusName: Record<string, string> = {
  active: "Активен",
  disabled: "Выключен",
  expired: "Срок истёк",
  limited: "Трафик исчерпан",
};
