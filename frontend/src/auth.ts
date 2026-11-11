// API token storage. The server no longer embeds the token in index.html
// (that page is served without auth, so embedding it handed the token to
// anyone who could load the page). The user enters it once; it is kept in
// this browser's localStorage.

const KEY = 'rollarr_api_token';
export const UNAUTHORIZED_EVENT = 'rollarr:unauthorized';

export function getToken(): string {
  try {
    return localStorage.getItem(KEY) ?? '';
  } catch {
    return '';
  }
}

export function setToken(token: string): void {
  try {
    localStorage.setItem(KEY, token);
  } catch {
    // Private mode or blocked storage: the token lasts until reload.
  }
}

export function clearToken(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // ignore
  }
}

/** Tell the app shell that the API rejected the token. */
export function signalUnauthorized(): void {
  window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
}
