import { useState } from "react";
import { api, type Me } from "../api";
import { ErrorBanner, Field, useAction } from "../ui";

export function LoginForm({ onDone }: { onDone: (me: Me) => void }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const a = useAction();
  return (
    <form
      className="card auth"
      onSubmit={(e) => {
        e.preventDefault();
        a.run(async () => onDone(await api.login(username, password)));
      }}
    >
      <h1>Вход</h1>
      <ErrorBanner error={a.error} />
      <Field label="Имя">
        <input autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
      </Field>
      <Field label="Пароль" hint="Забыли? На сервере: eva-manager admin passwd">
        <input type="password" autoFocus autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <button className="primary" type="submit" disabled={a.busy}>
        Войти
      </button>
    </form>
  );
}

// Мастер первого запуска: администратор создаётся один раз, и только тем,
// у кого есть токен с сервера.
export function SetupForm({ onDone }: { onDone: (me: Me) => void }) {
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const a = useAction();
  return (
    <form
      className="card auth"
      onSubmit={(e) => {
        e.preventDefault();
        if (password !== repeat) return a.setError("Пароли не совпали");
        a.run(async () => onDone(await api.setup(username, password, token)));
      }}
    >
      <h1>Первый запуск</h1>
      <p className="muted">
        Создайте администратора. Чтобы подтвердить, что панель настраивает хозяин сервера, вставьте токен — его печатает
        команда <code>eva-manager token</code> (в Docker: <code>docker compose exec eva-manager eva-manager token</code>).
      </p>
      <ErrorBanner error={a.error} />
      <Field label="Токен с сервера">
        <input type="password" autoFocus value={token} onChange={(e) => setToken(e.target.value)} />
      </Field>
      <Field label="Имя администратора">
        <input autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
      </Field>
      <Field label="Пароль" hint="Не короче 10 символов">
        <input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <Field label="Ещё раз">
        <input type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
      </Field>
      <button className="primary" type="submit" disabled={a.busy}>
        Создать и войти
      </button>
    </form>
  );
}
