import QRCode from "qrcode";
import { useEffect, useState, type ReactNode } from "react";

// Мелкие общие элементы интерфейса.

export function useAsync<T>(load: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [n, setN] = useState(0);
  useEffect(() => {
    let alive = true;
    load()
      .then((d) => {
        if (alive) {
          setData(d);
          setError(null);
        }
      })
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, n]);
  return { data, error, reload: () => setN((x) => x + 1), setData };
}

export function Copy({ text, label = "Копировать" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="ghost small"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? "Скопировано" : label}
    </button>
  );
}

export function QR({ text }: { text: string }) {
  const [src, setSrc] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    // Ключи vpn:// длинные: низкая коррекция ошибок — меньше модулей, легче
    // сканировать с экрана.
    QRCode.toDataURL(text, { errorCorrectionLevel: "L", margin: 2, width: 440 })
      .then(setSrc)
      .catch((e: Error) => setErr(e.message));
  }, [text]);
  if (err) return <p className="problem">QR не построен: {err}</p>;
  return src ? <img className="qr" src={src} alt="QR-код" width={440} height={440} /> : null;
}

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal card" role="dialog" aria-label={title}>
        <div className="card-head">
          <h2 className="modal-title">{title}</h2>
          <button className="ghost small" onClick={onClose} aria-label="Закрыть">
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="muted small-text">{hint}</span>}
    </label>
  );
}

export function ErrorBanner({ error }: { error: string | null }) {
  return error ? <div className="banner error">{error}</div> : null;
}

// busy — кнопка, которая блокируется на время запроса и показывает ошибку.
export function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      return true;
    } catch (e) {
      setError((e as Error).message);
      return false;
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, setError, run };
}
