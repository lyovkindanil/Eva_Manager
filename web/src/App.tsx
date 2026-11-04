import { useEffect, useState } from "react";
import { api, Unauthorized, type Me } from "./api";
import { Connections } from "./pages/Connections";
import { LoginForm, SetupForm } from "./pages/Auth";
import { Overview } from "./pages/Overview";
import { SettingsPage } from "./pages/Settings";
import { UserDetail } from "./pages/UserDetail";
import { Users } from "./pages/Users";
import { go, useRoute } from "./route";
import { applyTheme, savedTheme, type Theme } from "./theme";
import { Wordmark } from "./Wordmark";

type Auth = { state: "loading" } | { state: "setup" } | { state: "login" } | { state: "in"; me: Me } | { state: "down"; error: string };

const nav = [
  { path: "", title: "Обзор" },
  { path: "users", title: "Пользователи" },
  { path: "connections", title: "Подключения" },
  { path: "settings", title: "Настройки" },
];

export function App() {
  const [auth, setAuth] = useState<Auth>({ state: "loading" });
  const [theme, setTheme] = useState<Theme>(savedTheme());
  const route = useRoute();

  const check = () =>
    api
      .me()
      .then((me) => setAuth({ state: "in", me }))
      .catch(async (e: Error) => {
        if (!(e instanceof Unauthorized)) return setAuth({ state: "down", error: e.message });
        const s = await api.setupNeeded().catch(() => ({ needed: false }));
        setAuth({ state: s.needed ? "setup" : "login" });
      });

  useEffect(() => {
    check();
    // Сессия истекла в другой вкладке или пароль сменили с сервера.
    const on = () => check();
    window.addEventListener("eva-unauthorized", on);
    return () => window.removeEventListener("eva-unauthorized", on);
  }, []);

  const toggleTheme = () => {
    const t = theme === "dark" ? "light" : "dark";
    applyTheme(t);
    setTheme(t);
  };

  const section = route[0] ?? "";
  let page = null;
  if (auth.state === "in") {
    if (section === "users" && route[1]) page = <UserDetail id={route[1]} />;
    else if (section === "users") page = <Users />;
    else if (section === "connections") page = <Connections />;
    else if (section === "settings")
      page = (
        <SettingsPage
          me={auth.me}
          onLogout={() => api.logout().finally(() => setAuth({ state: "login" }))}
        />
      );
    else page = <Overview />;
  }

  return (
    <div className="app">
      <header className="top">
        <div className="brand">
          <Wordmark className="wordmark" />
          <span className="brand-name">Manager</span>
        </div>
        {auth.state === "in" && (
          <nav className="nav">
            {nav.map((n) => (
              <a key={n.path} href={"#/" + n.path} className={section === n.path ? "active" : ""}>
                {n.title}
              </a>
            ))}
          </nav>
        )}
        <button className="ghost" onClick={toggleTheme} title="Сменить тему">
          {theme === "dark" ? "Светлая" : "Тёмная"}
        </button>
      </header>

      <main className="content">
        {auth.state === "loading" && <p className="muted">Загрузка…</p>}
        {auth.state === "down" && <div className="banner error">Панель не отвечает: {auth.error}</div>}
        {auth.state === "setup" && <SetupForm onDone={(me) => (setAuth({ state: "in", me }), go(""))} />}
        {auth.state === "login" && <LoginForm onDone={(me) => setAuth({ state: "in", me })} />}
        {page}
      </main>
    </div>
  );
}
