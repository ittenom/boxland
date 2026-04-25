// Boxland — settings/store.ts
//
// Two-layer settings persistence:
//   1. localStorage  -- instant, offline-friendly
//   2. server (HTTP) -- authoritative across devices
//
// Load sequence: read localStorage first (fast, never blocks the
// renderer), then fire-and-forget the server fetch and reconcile if
// the server copy is newer. Save sequence: write localStorage
// immediately, debounce the PUT.

import { coerceSettings, DEFAULT_SETTINGS, type Settings } from "./types";

/** localStorage key. Per-realm so designer + player don't clobber. */
export function lsKey(realm: "designer" | "player"): string {
	return `boxland.settings.${realm}`;
}

/** Load from localStorage; falls back to defaults on parse failure. */
export function loadLocal(realm: "designer" | "player"): Settings {
	if (typeof localStorage === "undefined") return { ...DEFAULT_SETTINGS };
	try {
		const raw = localStorage.getItem(lsKey(realm));
		if (!raw) return { ...DEFAULT_SETTINGS };
		return coerceSettings(JSON.parse(raw));
	} catch {
		return { ...DEFAULT_SETTINGS };
	}
}

/** Persist to localStorage. Synchronous; no throw. */
export function saveLocal(realm: "designer" | "player", s: Settings): void {
	if (typeof localStorage === "undefined") return;
	try {
		localStorage.setItem(lsKey(realm), JSON.stringify(s));
	} catch {
		/* quota exceeded etc. -- silent for v1 */
	}
}

/** Fetch from the server, coerce. Returns DEFAULT_SETTINGS on any
 *  failure; the caller decides whether to surface that to the user. */
export async function loadRemote(loadURL: string): Promise<Settings> {
	try {
		const res = await fetch(loadURL, { credentials: "same-origin" });
		if (!res.ok) return { ...DEFAULT_SETTINGS };
		const body = await res.json();
		return coerceSettings(body);
	} catch {
		return { ...DEFAULT_SETTINGS };
	}
}

/** PUT the entire blob. Returns true on 204 / 200, false otherwise. */
export async function saveRemote(saveURL: string, s: Settings, csrfToken?: string): Promise<boolean> {
	try {
		const headers: Record<string, string> = { "Content-Type": "application/json" };
		if (csrfToken) headers["X-CSRF-Token"] = csrfToken;
		const res = await fetch(saveURL, {
			method: "PUT",
			credentials: "same-origin",
			headers,
			body: JSON.stringify(s),
		});
		return res.ok;
	} catch {
		return false;
	}
}

/** Read the CSRF token from the Templ-emitted meta tag. Mirrors the
 *  static/js/boot.js behavior so HTMX + this module use the same value. */
export function readCSRFToken(): string | undefined {
	if (typeof document === "undefined") return undefined;
	const meta = document.querySelector('meta[name="csrf-token"]');
	const tok = meta?.getAttribute("content") ?? undefined;
	return tok || undefined;
}

/** Debounced write to the server. Keyed by saveURL so the same hook
 *  works for both realms; per-key timer cancels previous pending
 *  PUTs. Returns a `flush` callable for tests + page-unload. */
export function makeRemoteSaver(saveURL: string, debounceMs = 500): {
	schedule: (s: Settings) => void;
	flush: () => Promise<boolean>;
	cancel: () => void;
} {
	let timer: ReturnType<typeof setTimeout> | null = null;
	let pending: Settings | null = null;

	const flush = async (): Promise<boolean> => {
		if (timer) { clearTimeout(timer); timer = null; }
		if (!pending) return true;
		const out = pending;
		pending = null;
		return saveRemote(saveURL, out, readCSRFToken());
	};

	return {
		schedule(s: Settings) {
			pending = s;
			if (timer) clearTimeout(timer);
			timer = setTimeout(() => { void flush(); }, debounceMs);
		},
		flush,
		cancel() { if (timer) { clearTimeout(timer); timer = null; } pending = null; },
	};
}
