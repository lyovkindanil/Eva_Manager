import { useState } from "react";
import { api, type AWG, type Inbound } from "../api";
import { ErrorBanner, Field, Modal, useAction, useAsync } from "../ui";

// Подключения узла: инбаунды Xray (VLESS Reality) и интерфейсы AmneziaWG.
export function Connections() {
  const inbounds = useAsync(api.inbounds, []);
  const awgs = useAsync(api.awg, []);
  const [adding, setAdding] = useState<"vless" | "awg" | null>(null);
  const act = useAction();
  const [notice, setNotice] = useState<string | null>(null);

  const applied = (r: { applied: boolean; error?: string }) => {
    setNotice(r.error ? `Сохранено, но Xray не принял конфиг: ${r.error}` : r.applied ? null : "Сохранено. Xray не найден — применится при его запуске.");
  };

  return (
    <>
      <div className="page-head">
        <h1>Подключения</h1>
        <div className="actions">
          <button className="primary" onClick={() => setAdding("vless")}>
            + VLESS Reality
          </button>
          <button className="primary" onClick={() => setAdding("awg")}>
            + AmneziaWG
          </button>
        </div>
      </div>
      <ErrorBanner error={inbounds.error || awgs.error || act.error} />
      {notice && <div className="banner warn">{notice}</div>}
      <p className="muted">
        Каждый пользователь получает доступ ко всем включённым подключениям. Не забудьте открыть порты на сервере: VLESS — TCP,
        AmneziaWG — UDP.
      </p>

      <h2>VLESS Reality</h2>
      {inbounds.data?.length === 0 && <div className="card empty muted">Нет инбаундов.</div>}
      {inbounds.data && inbounds.data.length > 0 && (
        <div className="card list">
          {inbounds.data.map((i) => (
            <InboundRow
              key={i.id}
              i={i}
              busy={act.busy}
              onToggle={() =>
                act.run(async () => {
                  applied(await api.patchInbound(i.id, !i.enabled));
                  inbounds.reload();
                })
              }
              onDelete={() =>
                confirm(`Удалить инбаунд ${i.name}? Ссылки пользователей на него перестанут работать.`) &&
                act.run(async () => {
                  applied(await api.deleteInbound(i.id));
                  inbounds.reload();
                })
              }
            />
          ))}
        </div>
      )}

      <h2>AmneziaWG</h2>
      {awgs.data?.length === 0 && <div className="card empty muted">Нет интерфейсов.</div>}
      {awgs.data && awgs.data.length > 0 && (
        <div className="card list">
          {awgs.data.map((a) => (
            <AWGRow
              key={a.id}
              a={a}
              busy={act.busy}
              onToggle={() =>
                act.run(async () => {
                  await api.patchAWG(a.id, !a.enabled);
                  awgs.reload();
                })
              }
              onDelete={() =>
                confirm(`Удалить ${a.name}? Ключи пользователей на нём перестанут работать.`) &&
                act.run(async () => {
                  await api.deleteAWG(a.id);
                  awgs.reload();
                })
              }
            />
          ))}
        </div>
      )}
      {adding === "vless" && (
        <AddInbound
          onClose={() => setAdding(null)}
          onDone={(r) => {
            applied(r);
            setAdding(null);
            inbounds.reload();
          }}
        />
      )}
      {adding === "awg" && (
        <AddAWG
          onClose={() => setAdding(null)}
          onDone={() => {
            setAdding(null);
            awgs.reload();
          }}
        />
      )}
    </>
  );
}

function InboundRow({ i, busy, onToggle, onDelete }: { i: Inbound; busy: boolean; onToggle: () => void; onDelete: () => void }) {
  return (
    <div className="row">
      <span className={`dot ${i.enabled ? "on" : "off"}`} />
      <span className="grow">
        <span className="row-title">{i.name}</span>
        <span className="muted">
          {" "}
          · {i.host}:{i.port} · маскировка {i.dest}
          {i.encryption && " · VLESS Encryption"}
        </span>
      </span>
      <button className="ghost small" disabled={busy} onClick={onToggle}>
        {i.enabled ? "Выключить" : "Включить"}
      </button>
      <button className="ghost small danger-text" disabled={busy} onClick={onDelete}>
        Удалить
      </button>
    </div>
  );
}

function AWGRow({ a, busy, onToggle, onDelete }: { a: AWG; busy: boolean; onToggle: () => void; onDelete: () => void }) {
  const state = !a.enabled ? "выключен" : a.running ? "работает" : "запускается";
  return (
    <div className="row wrap">
      <span className={`dot ${a.enabled && a.running ? "on" : "off"}`} />
      <span className="grow">
        <span className="row-title">{a.name}</span>
        <span className="muted">
          {" "}
          · {a.host}:{a.port}/udp · {a.iface} · {a.subnet} · {state}
        </span>
        {a.problem && <span className="problem block">{a.problem}</span>}
      </span>
      <button className="ghost small" disabled={busy} onClick={onToggle}>
        {a.enabled ? "Выключить" : "Включить"}
      </button>
      <button className="ghost small danger-text" disabled={busy} onClick={onDelete}>
        Удалить
      </button>
    </div>
  );
}

// Адрес сервера по умолчанию — тот, по которому открыта панель (если это
// не 127.0.0.1 через SSH-туннель).
function defaultHost() {
  const h = window.location.hostname;
  return h === "localhost" || h === "127.0.0.1" || h === "::1" ? "" : h;
}

function AddInbound({ onClose, onDone }: { onClose: () => void; onDone: (r: { applied: boolean; error?: string }) => void }) {
  const [name, setName] = useState("");
  const [host, setHost] = useState(defaultHost());
  const [port, setPort] = useState("");
  const [dest, setDest] = useState("");
  const [enc, setEnc] = useState(false);
  const a = useAction();
  return (
    <Modal title="VLESS Reality" onClose={onClose}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          a.run(async () => onDone(await api.createInbound({ name: name.trim(), host: host.trim(), port: port ? Number(port) : 0, dest: dest.trim(), encryption: enc })));
        }}
      >
        <ErrorBanner error={a.error} />
        <Field label="Название" hint="Например, NL-1 — видно у клиентов">
          <input autoFocus required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Адрес сервера" hint="IP или домен, по которому клиенты подключаются">
          <input required value={host} onChange={(e) => setHost(e.target.value)} placeholder="vpn.example.com" />
        </Field>
        <div className="two">
          <Field label="Порт (TCP)" hint="Пусто — 443, а если он занят другой программой, 8443, 2053…">
            <input inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} placeholder="443" />
          </Field>
          <Field label="Сайт маскировки" hint="Пусто — из проверенного списка">
            <input value={dest} onChange={(e) => setDest(e.target.value)} placeholder="www.example.com:443" />
          </Field>
        </div>
        <label className="check">
          <input type="checkbox" checked={enc} onChange={(e) => setEnc(e.target.checked)} />
          <span>
            VLESS Encryption (постквантовое шифрование поверх Reality). Нужен клиент с его поддержкой — VPN4Eva или Xray 25.9+.
          </span>
        </label>
        <button className="primary" disabled={a.busy}>
          {a.busy ? "Проверка конфига и перезапуск Xray…" : "Создать"}
        </button>
      </form>
    </Modal>
  );
}

function AddAWG({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [host, setHost] = useState(defaultHost());
  const [port, setPort] = useState("");
  const [subnet, setSubnet] = useState("");
  const a = useAction();
  return (
    <Modal title="AmneziaWG" onClose={onClose}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          a.run(async () => {
            await api.createAWG({ name: name.trim(), host: host.trim(), port: port ? Number(port) : 0, subnet: subnet.trim() });
            onDone();
          });
        }}
      >
        <ErrorBanner error={a.error} />
        <p className="muted">
          Ключи и маскировка AmneziaWG 2.0 создаются автоматически. Пользователи получат ключ <code>vpn://</code> и файл
          .conf.
        </p>
        <Field label="Название" hint="Пусто — AmneziaWG">
          <input autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Адрес сервера">
          <input required value={host} onChange={(e) => setHost(e.target.value)} placeholder="vpn.example.com" />
        </Field>
        <div className="two">
          <Field label="Порт (UDP)" hint="Пусто — 51820 или следующий свободный в системе">
            <input inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} />
          </Field>
          <Field label="Подсеть клиентов" hint="Пусто — первая свободная от 10.8.0.0/24 (не пересекается с wg-easy и другими VPN)">
            <input value={subnet} onChange={(e) => setSubnet(e.target.value)} />
          </Field>
        </div>
        <button className="primary" disabled={a.busy}>
          Создать
        </button>
      </form>
    </Modal>
  );
}
