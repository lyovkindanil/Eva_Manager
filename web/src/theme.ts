// Тема: тёмная по умолчанию, как у клиента VPN4Eva; выбор запоминается.

export type Theme = "dark" | "light";

const key = "eva-manager.theme";

export function savedTheme(): Theme {
  try {
    return localStorage.getItem(key) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

export function applyTheme(t: Theme) {
  document.documentElement.dataset.theme = t;
  try {
    localStorage.setItem(key, t);
  } catch {
    // без хранилища тема просто не запомнится
  }
}
