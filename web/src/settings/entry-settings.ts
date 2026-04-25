// Boxland — settings/entry-settings.ts
//
// Page boot for /design/settings + /play/settings. Hydrates the
// Templ-rendered form with the user's current settings, wires the
// font picker / audio sliders / spectator checkbox / rebinder,
// debounces saves to the server, applies changes live.

import { CommandBus } from "@command-bus";

import { coerceSettings, FONT_OPTIONS, type Settings } from "./types";
import { loadLocal, saveLocal, loadRemote, makeRemoteSaver } from "./store";
import { applyFont, applySpectator, applyAudio, type VolumeApplier } from "./apply";
import { renderRebinderRows } from "./rebinder";

/** Mount the Settings UI. Returns a dispose() for tests; production
 *  pages don't call it (the page lives until navigation). */
export function bootSettings(root: HTMLElement = document.getElementById("bx-settings") as HTMLElement): (() => void) | null {
	if (!root) return null;
	const realm = (root.dataset.bxRealm === "designer" ? "designer" : "player") as "designer" | "player";
	const loadURL = root.dataset.bxLoadUrl ?? "";
	const saveURL = root.dataset.bxSaveUrl ?? "";
	const status = root.querySelector("[data-bx-status]") as HTMLElement | null;

	let state: Settings = loadLocal(realm);
	const saver = makeRemoteSaver(saveURL);

	const setStatus = (msg: string): void => { if (status) status.textContent = msg; };

	const persist = (): void => {
		saveLocal(realm, state);
		saver.schedule(state);
		setStatus("saving…");
	};

	// Hydrate from the server, reconcile if newer.
	if (loadURL) {
		void loadRemote(loadURL).then((remote) => {
			// For v1 the server is the source of truth on every page
			// load; the localStorage copy is just to avoid a flash.
			state = coerceSettings({ ...state, ...remote });
			saveLocal(realm, state);
			applyFromState();
			renderControls();
			setStatus("ready");
		});
	}

	// ---- Font picker ----
	const fontPicker = root.querySelector("[data-bx-font-picker]") as HTMLElement | null;
	if (fontPicker) {
		fontPicker.addEventListener("click", (e) => {
			const btn = (e.target as HTMLElement).closest("[data-bx-font]") as HTMLElement | null;
			if (!btn) return;
			const f = btn.dataset.bxFont ?? "";
			if (!(FONT_OPTIONS as readonly string[]).includes(f)) return;
			state.font = f as Settings["font"];
			applyFont(state.font);
			markActiveFont();
			persist();
		});
	}

	// ---- Audio sliders ----
	// If a sound engine is exposed by the active page (game/sandbox),
	// apply the slider value to its gain buses so the user hears the
	// change immediately. Settings pages outside a game session just
	// store the value -- it'll apply next time a game boots.
	const volume = (globalThis as unknown as { boxlandAudio?: VolumeApplier }).boxlandAudio;
	const sliders = root.querySelectorAll("[data-bx-audio-slider]");
	sliders.forEach((el) => {
		const slider = el as HTMLInputElement;
		const key = slider.dataset.bxAudioSlider as keyof Settings["audio"] | undefined;
		if (!key) return;
		const out = root.querySelector(`[data-bx-audio-output="${key}"]`) as HTMLElement | null;
		slider.addEventListener("input", () => {
			const v = Math.max(0, Math.min(100, Number(slider.value)));
			state.audio[key] = v;
			if (out) out.textContent = String(v);
			if (volume) applyAudio(state, volume);
			persist();
		});
	});

	// ---- Spectator preference ----
	const spec = root.querySelector("[data-bx-spectator-freecam]") as HTMLInputElement | null;
	if (spec) {
		spec.addEventListener("change", () => {
			state.spectator.freeCam = spec.checked;
			applySpectator(state.spectator.freeCam);
			persist();
		});
	}

	// ---- Rebinder ----
	// We surface every command from a "shared" CommandBus stamped on
	// `window.boxlandBus` by the host page. If no host bus is provided
	// the rebinder shows the empty state.
	const rebinderTbody = root.querySelector("[data-bx-rebinder-rows]") as HTMLElement | null;
	const sharedBus: CommandBus | undefined = (globalThis as unknown as { boxlandBus?: CommandBus }).boxlandBus;

	const rerenderRebinder = (): void => {
		if (!rebinderTbody || !sharedBus) return;
		renderRebinderRows(rebinderTbody, sharedBus, state.bindings, (combo, commandId) => {
			// Drop any prior binding pointing at this command id, then
			// either record the new one or clear (combo = null).
			for (const existing of Object.keys(state.bindings)) {
				if (state.bindings[existing] === commandId) delete state.bindings[existing];
			}
			if (combo) state.bindings[combo] = commandId;
			persist();
		});
	};

	// ---- Reset to defaults ----
	const resetBtn = root.querySelector("[data-bx-reset]") as HTMLButtonElement | null;
	if (resetBtn) {
		resetBtn.addEventListener("click", () => {
			state = coerceSettings({});
			applyFromState();
			renderControls();
			persist();
		});
	}

	// ---- helpers ----

	const markActiveFont = (): void => {
		root.querySelectorAll("[data-bx-font]").forEach((b) => {
			const el = b as HTMLElement;
			if (el.dataset.bxFont === state.font) {
				el.setAttribute("aria-current", "true");
			} else {
				el.removeAttribute("aria-current");
			}
		});
	};

	const applyFromState = (): void => {
		applyFont(state.font);
		applySpectator(state.spectator.freeCam);
	};
	const renderControls = (): void => {
		// Sync slider values + spectator checkbox + active font marker.
		root.querySelectorAll("[data-bx-audio-slider]").forEach((el) => {
			const slider = el as HTMLInputElement;
			const key = slider.dataset.bxAudioSlider as keyof Settings["audio"] | undefined;
			if (!key) return;
			slider.value = String(state.audio[key]);
			const out = root.querySelector(`[data-bx-audio-output="${key}"]`) as HTMLElement | null;
			if (out) out.textContent = String(state.audio[key]);
		});
		if (spec) spec.checked = state.spectator.freeCam;
		markActiveFont();
		rerenderRebinder();
	};

	// Initial paint from local cache while the server fetch races.
	applyFromState();
	renderControls();

	// Flush the debounced saver before unload.
	const onUnload = (): void => { void saver.flush(); };
	window.addEventListener("pagehide", onUnload);
	return () => {
		window.removeEventListener("pagehide", onUnload);
		saver.cancel();
	};
}

if (typeof document !== "undefined" && document.body?.dataset.surface === "settings") {
	bootSettings();
}
