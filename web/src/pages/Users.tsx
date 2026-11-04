import { useState } from "react";
import { api, type User } from "../api";
import { bytes, date, daysLeft, GB, statusName } from "../format";
import { go } from "../route";
import { ErrorBanner, Field, Modal, useAction, useAsync } from "../ui";

export function Users() {
  const users = useAsync(api.users, []);
  const [adding, setAdding] = useState(false);
  const [q, setQ] = useState("");
  const list = (users.data ?? []).filter((u) => !q || u.name.toLowerCase().includes(q.toLowerCase()) || u.note.toLowerCase().includes(q.toLowerCase()));
  return (
    <>
      <div className="page-head">
        <h1>Пользователи</h1>
        <button className="primary" onClick={() => setAdding(true)}>
          Добавить
        </button>
      </div>
      <ErrorBanner error={users.error} />
      {users.data && users.data.length > 5 && (
        <input className="search" placeholder="Поиск по имени или заметке" value={q} onChange={(e) => setQ(e.target.value)} />
      )}
      {users.data && users.data.length === 0 && (
        <div className="card empty">
          <p>Пользователей пока нет.</p>
          <p className="muted">Добавьте первого: он сразу получит ссылки на все включённые подключения и подписку.</p>
        </div>
      )}
      {list.length > 0 && (
        <div className="card list">
          {list.map((u) => (
            <UserRow key={u.id} u={u} />
          ))}
        </div>
      )}
      {adding && <AddUser onClose={() => setAdding(false)} />}
    </>
  );
}

function UserRow({ u }: { u: User }) {
  const left = daysLeft(u.expires_at);
  const pct = u.traffic_limit > 0 ? Math.min(100, (u.traffic_used / u.traffic_limit) * 100) : 0;
  return (
    <a className="row link-row" href={`#/users/${u.id}`}>
      <span className={`dot ${u.status === "active" ? "on" : "off"}`} />
      <span className="grow">
        <span className="row-title">{u.name}</span>
        {u.note && <span className="muted"> · {u.note}</span>}
        {u.status !== "active" && <span className="pill warn-pill">{statusName[u.status]}</span>}
      </span>
      <span className="traffic">
        <span>
          {bytes(u.traffic_used)}
          <span className="muted"> / {u.traffic_limit > 0 ? bytes(u.traffic_limit) : "∞"}</span>
        </span>
        {u.traffic_limit > 0 && (
          <span className="bar">
            <span style={{ width: `${pct}%` }} className={pct >= 90 ? "hot" : ""} />
          </span>
        )}
      </span>
      <span className="muted until">{left === null ? "бессрочно" : left > 0 ? `ещё ${left} дн.` : `до ${date(u.expires_at)}`}</span>
    </a>
  );
}

// AddUser — имя, лимит и срок; после создания — карточка пользователя со
// ссылками.
function AddUser({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [limit, setLimit] = useState("");
  const [days, setDays] = useState("30");
  const a = useAction();
  return (
    <Modal title="Новый пользователь" onClose={onClose}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          const d = Number(days);
          a.run(async () => {
            const u = await api.createUser({
              name: name.trim(),
              note: note.trim(),
              traffic_limit: limit ? Math.round(Number(limit.replace(",", ".")) * GB) : 0,
              expires_at: d > 0 ? new Date(Date.now() + d * 86400000).toISOString().replace(/\.\d+Z$/, "Z") : "",
            });
            go(`users/${u.id}`);
          });
        }}
      >
        <ErrorBanner error={a.error} />
        <Field label="Имя" hint="Видно в названиях подключений у клиента">
          <input autoFocus required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <div className="two">
          <Field label="Лимит, ГБ" hint="Пусто — без лимита">
            <input inputMode="decimal" value={limit} onChange={(e) => setLimit(e.target.value)} placeholder="∞" />
          </Field>
          <Field label="Срок, дней" hint="0 — бессрочно">
            <input inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value)} />
          </Field>
        </div>
        <Field label="Заметка">
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Например, телефон мамы" />
        </Field>
        <button className="primary" disabled={a.busy || !name.trim()}>
          Создать
        </button>
      </form>
    </Modal>
  );
}
