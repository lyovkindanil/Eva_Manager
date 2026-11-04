import { useEffect, useState } from "react";
import { api, type Me } from "../api";
import { ErrorBanner, Field, useAction, useAsync } from "../ui";

export function SettingsPage({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const s = useAsync(api.settings, []);
  const [sub, setSub] = useState("");
  const [saved, setSaved] = useState(false);
  const a = useAction();
  useEffect(() => {
    if (s.data) setSub(s.data.sub_url);
  }, [s.data]);
  return (
    <>
      <h1>Настройки</h1>
      <ErrorBanner error={s.error || a.error} />
      <h2>Подписки</h2>
      <form
        className="card form"
        onSubmit={(e) => {
          e.preventDefault();
          a.run(async () => {
            s.setData(await api.putSettings({ sub_url: sub.trim() }));
            setSaved(true);
            setTimeout(() => setSaved(false), 2000);
          });
        }}
      >
        <Field
          label="Внешний адрес подписок"
          hint={
            s.data?.sub_url_from_env
              ? "Задан в EVA_SUB_URL на сервере."
              : "Например, https://vpn.example.com:2096. Пусто — адрес, по которому открыта панель."
          }
        >
          <input value={sub} disabled={s.data?.sub_url_from_env} onChange={(e) => setSub(e.target.value)} placeholder="https://vpn.example.com:2096" />
        </Field>
        {!s.data?.sub_url_from_env && (
          <button className="primary self-start" disabled={a.busy}>
            {saved ? "Сохранено" : "Сохранить"}
          </button>
        )}
      </form>

      <h2>Администратор</h2>
      <div className="card">
        <p>
          {me.via === "session" ? (
            <>
              Вы вошли как <b>{me.username}</b>.
            </>
          ) : (
            "Вход по токену."
          )}
        </p>
        <p className="muted">
          Сменить пароль: на сервере <code>eva-manager admin passwd --name {me.username || "admin"}</code> — все сессии
          закроются.
        </p>
        <button className="ghost self-start" onClick={onLogout}>
          Выйти
        </button>
      </div>
    </>
  );
}
