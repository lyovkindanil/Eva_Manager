import { useEffect, useState } from "react";

// Маршруты в hash (#/users/…): сервер отдаёт index.html на любой путь,
// но hash не уходит в запросы и не попадает в журналы прокси.
export function useRoute(): string[] {
  const parse = () => window.location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const [r, setR] = useState(parse);
  useEffect(() => {
    const on = () => setR(parse());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return r;
}

export function go(path: string) {
  window.location.hash = "#/" + path.replace(/^\//, "");
}
