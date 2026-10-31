import { useEffect, useState } from "react";
import { api, type Status, type XrayStatus } from "./api";
import { dbName, duration } from "./format";
import { applyTheme, savedTheme, type Theme } from "./theme";
import { Wordmark } from "./Wordmark";

// Этап 0: обзор состояния панели. Пользователи, инбаунды и настройки
// появятся на следующих этапах.
export function App() {
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [theme, setTheme] = useState<Theme>(savedTheme());

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .status()
        .then((s) => {
          if (!alive) return;
          setStatus(s);
          setError(null);
        })
        .catch((e: Error) => alive && setError(e.message));
    load();
    const id = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const toggleTheme = () => {
    const t = theme === "dark" ? "light" : "dark";
    applyTheme(t);
    setTheme(t);
  };

  return (
    <div className="app">
      <header className="top">
        <div className="brand">
          <Wordmark className="wordmark" />
          <span className="brand-name">Manager</span>
        </div>
        <button className="ghost" onClick={toggleTheme} title="Сменить тему">
          {theme === "dark" ? "Светлая тема" : "Тёмная тема"}
        </button>
      </header>

      <main className="content">
        <h1>Обзор</h1>
        {error && <div className="banner error">Панель не отвечает: {error}</div>}
        {!status && !error && <p className="muted">Загрузка…</p>}
        {status && (
          <>
            <div className="grid">
              <XrayCard x={status.xray} />
              <div className="card">
                <span className="label">База данных</span>
                <span className="value">{dbName(status.database.dialect)}</span>
                <span className="muted">Схема версии {status.database.schema}</span>
              </div>
              <div className="card">
                <span className="label">Панель</span>
                <span className="value">{status.version}</span>
                <span className="muted">Eva Manager</span>
              </div>
            </div>

            <h2>Узлы</h2>
            <div className="card list">
              {status.nodes.map((n) => (
                <div className="row" key={n.id}>
                  <span className={`dot ${n.is_local && status.xray.running ? "on" : "off"}`} />
                  <span className="grow">{n.name}</span>
                  <span className="muted">{n.is_local ? "локальный" : n.address}</span>
                </div>
              ))}
            </div>

            <XrayLogs />
          </>
        )}
      </main>
    </div>
  );
}

function XrayCard({ x }: { x: XrayStatus }) {
  const ok = x.running && x.api && !x.problem;
  const state = !x.installed ? "Не установлен" : ok ? "Работает" : x.running ? "Запущен с ошибкой" : "Остановлен";
  return (
    <div className={`card ${ok ? "" : "warn"}`}>
      <div className="card-head">
        <span className="label">Xray-core</span>
        <span className={`pill ${ok ? "on" : "off"}`}>{state}</span>
      </div>
      <span className="value">{x.version ?? "—"}</span>
      {ok && x.uptime_s !== undefined && <span className="muted">Работает {duration(x.uptime_s)}</span>}
      {x.problem && <span className="problem">{x.problem}</span>}
    </div>
  );
}

function XrayLogs() {
  const [open, setOpen] = useState(false);
  const [lines, setLines] = useState<string[]>([]);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    const load = () =>
      api
        .xrayLogs()
        .then((r) => alive && setLines(r.lines))
        .catch(() => {});
    load();
    const id = setInterval(load, 3000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [open]);

  return (
    <section>
      <button className="ghost section-toggle" onClick={() => setOpen(!open)}>
        {open ? "▾" : "▸"} Журнал Xray
      </button>
      {open && <pre className="logs">{lines.length ? lines.join("\n") : "Журнал пуст"}</pre>}
    </section>
  );
}
