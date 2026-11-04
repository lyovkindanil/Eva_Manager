import { useState } from "react";
import { api, type Access, type UserView } from "../api";
import { bytes, date, daysLeft, GB, isForever, statusName } from "../format";
import { go } from "../route";
import { Copy, ErrorBanner, Field, Modal, QR, useAction, useAsync } from "../ui";

export function UserDetail({ id }: { id: string }) {
  const user = useAsync(() => api.user(id), [id]);
  const traffic = useAsync(() => api.traffic(id, 14), [id]);
  const [editing, setEditing] = useState(false);
  const [qr, setQR] = useState<{ title: string; text: string } | null>(null);
  const act = useAction();
  const u = user.data;

  const doAct = (fn: () => Promise<UserView>) => act.run(async () => user.setData(await fn()));

  if (user.error) return <ErrorBanner error={user.error} />;
  if (!u) return <p className="muted">Загрузка…</p>;
  const left = daysLeft(u.expires_at);
  return (
    <>
      <a className="back" href="#/users">
        ← Пользователи
      </a>
      <div className="page-head">
        <h1>
          {u.name} <span className={`pill ${u.status === "active" ? "on" : "off"}`}>{statusName[u.status]}</span>
        </h1>
        <div className="actions">
          <button className="ghost" onClick={() => setEditing(true)}>
            Изменить
          </button>
          <button className="ghost" disabled={act.busy} onClick={() => doAct(() => api.patchUser(u.id, { enabled: u.status === "disabled" }))}>
            {u.status === "disabled" ? "Включить" : "Выключить"}
          </button>
        </div>
      </div>
      <ErrorBanner error={act.error} />
      {u.note && <p className="muted">{u.note}</p>}

      <div className="grid">
        <div className="card">
          <span className="label">Трафик</span>
          <span className="value">
            {bytes(u.traffic_used)} <span className="muted">/ {u.traffic_limit > 0 ? bytes(u.traffic_limit) : "∞"}</span>
          </span>
          <button className="ghost small self-start" disabled={act.busy || u.traffic_used === 0} onClick={() => doAct(() => api.resetTraffic(u.id))}>
            Сбросить счётчик
          </button>
        </div>
        <div className="card">
          <span className="label">Срок</span>
          <span className="value">{date(u.expires_at)}</span>
          <span className="muted">{left === null ? "без ограничения" : left > 0 ? `осталось ${left} дн.` : "истёк"}</span>
        </div>
      </div>

      <h2>Подписка</h2>
      <div className="card">
        <p className="muted">
          Одна ссылка на все подключения: её понимают VPN4Eva, v2rayN, Hiddify и другие клиенты. Трафик и срок клиент
          показывает сам.
        </p>
        <code className="link">{u.sub_url}</code>
        <div className="actions">
          <Copy text={u.sub_url} />
          <button className="ghost small" onClick={() => setQR({ title: "Подписка", text: u.sub_url })}>
            QR-код
          </button>
          <button
            className="ghost small"
            disabled={act.busy}
            onClick={() =>
              confirm("Старая ссылка подписки перестанет работать. Перевыпустить?") && doAct(() => api.rotateSub(u.id))
            }
          >
            Перевыпустить
          </button>
        </div>
      </div>

      <h2>Подключения</h2>
      {u.access.length === 0 && (
        <div className="card empty">
          <p className="muted">
            Включённых подключений нет. Добавьте их в разделе <a href="#/connections">Подключения</a>.
          </p>
        </div>
      )}
      {u.access.map((a) => (
        <AccessCard key={a.kind + a.name} u={u} a={a} onQR={() => setQR({ title: a.name, text: a.link })} />
      ))}

      {traffic.data && traffic.data.length > 0 && (
        <>
          <h2>По дням</h2>
          <div className="card list">
            {traffic.data.map((d) => (
              <div className="row" key={d.day}>
                <span className="grow">{new Date(d.day).toLocaleDateString("ru-RU", { day: "numeric", month: "short" })}</span>
                <span className="muted">↑ {bytes(d.uplink)}</span>
                <span>↓ {bytes(d.downlink)}</span>
              </div>
            ))}
          </div>
        </>
      )}

      <h2>Опасная зона</h2>
      <button
        className="danger"
        onClick={() =>
          confirm(`Удалить ${u.name}? Ссылки и подписка перестанут работать.`) &&
          act.run(async () => {
            await api.deleteUser(u.id);
            go("users");
          })
        }
      >
        Удалить пользователя
      </button>

      {qr && (
        <Modal title={qr.title} onClose={() => setQR(null)}>
          <QR text={qr.text} />
          <p className="muted small-text">Отсканируйте в приложении: VPN4Eva, AmneziaVPN, v2rayNG, Hiddify.</p>
        </Modal>
      )}
      {editing && <EditUser u={u} onClose={() => setEditing(false)} onSaved={(v) => (user.setData(v), setEditing(false))} />}
    </>
  );
}

function AccessCard({ u, a, onQR }: { u: UserView; a: Access; onQR: () => void }) {
  return (
    <div className="card access">
      <div className="card-head">
        <span>
          <span className="row-title">{a.name}</span>{" "}
          <span className="pill">{a.kind === "awg" ? "AmneziaWG" : "VLESS Reality"}</span>
        </span>
      </div>
      <code className="link">{a.link}</code>
      <div className="actions">
        <Copy text={a.link} />
        <button className="ghost small" onClick={onQR}>
          QR-код
        </button>
        {a.awg && (
          <a className="ghost small button-link" href={api.awgConfURL(u.id, a.awg)} download>
            Файл .conf
          </a>
        )}
      </div>
    </div>
  );
}

function EditUser({ u, onClose, onSaved }: { u: UserView; onClose: () => void; onSaved: (v: UserView) => void }) {
  const [note, setNote] = useState(u.note);
  const [limit, setLimit] = useState(u.traffic_limit > 0 ? String(+(u.traffic_limit / GB).toFixed(2)) : "");
  const [until, setUntil] = useState(isForever(u.expires_at) ? "" : u.expires_at.slice(0, 10));
  const a = useAction();
  const extend = (days: number) => {
    const base = isForever(u.expires_at) || new Date(u.expires_at) < new Date() ? new Date() : new Date(u.expires_at);
    base.setDate(base.getDate() + days);
    setUntil(base.toISOString().slice(0, 10));
  };
  return (
    <Modal title={`Изменить ${u.name}`} onClose={onClose}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          a.run(async () =>
            onSaved(
              await api.patchUser(u.id, {
                note: note.trim(),
                traffic_limit: limit ? Math.round(Number(limit.replace(",", ".")) * GB) : 0,
                // Конец выбранного дня по местному времени.
                expires_at: until ? new Date(until + "T23:59:59").toISOString().replace(/\.\d+Z$/, "Z") : "",
              }),
            ),
          );
        }}
      >
        <ErrorBanner error={a.error} />
        <Field label="Лимит, ГБ" hint="Пусто — без лимита">
          <input inputMode="decimal" value={limit} onChange={(e) => setLimit(e.target.value)} placeholder="∞" />
        </Field>
        <Field label="Действует до" hint="Пусто — бессрочно">
          <input type="date" value={until} onChange={(e) => setUntil(e.target.value)} />
        </Field>
        <div className="actions">
          <button type="button" className="ghost small" onClick={() => extend(30)}>
            +30 дней
          </button>
          <button type="button" className="ghost small" onClick={() => extend(90)}>
            +90 дней
          </button>
          <button type="button" className="ghost small" onClick={() => setUntil("")}>
            Бессрочно
          </button>
        </div>
        <Field label="Заметка">
          <input value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
        <button className="primary" disabled={a.busy}>
          Сохранить
        </button>
      </form>
    </Modal>
  );
}
