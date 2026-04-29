// Boxland — widget facade.
//
// Theme-skinned wrappers around `@pixi/ui` widgets. Editors pass a
// `Theme` + a few visual options; we return a Pixi Container ready
// to mount in a flexbox slot.
//
// All widgets here:
//   * mount via @pixi/layout (their `.layout` is set so flex
//     parents handle them naturally),
//   * skin via the theme so swapping art replaces every instance,
//   * expose typed events the surface entry script wires up.
//
// `@pixi/ui` covers Button (FancyButton), CheckBox, Slider,
// ScrollBox, Select, RadioGroup, Input, List. We re-export them
// + add factories that pre-skin them with our theme. Callers can
// also bypass the factories and instantiate the underlying
// `@pixi/ui` types directly when they need full control.

import "../editors/layout-init";
import { Container, Text, Texture } from "pixi.js";
import {
	FancyButton,
	CheckBox as PixiCheckBox,
	Slider as PixiSlider,
	Input as PixiInput,
	List as PixiList,
	ScrollBox as PixiScrollBox,
	Select as PixiSelect,
} from "@pixi/ui";

import { NineSlice } from "./nine-slice";
import { Roles, Theme, type Role } from "./theme";

// ---- Button ---------------------------------------------------------

export type ButtonSize = "sm" | "md" | "lg";

export interface ButtonOptions {
	theme: Theme;
	label: string;
	size?: ButtonSize;
	width?: number;
	height?: number;
	disabled?: boolean;
	onPress?: () => void | Promise<void>;
}

const buttonRoles: Record<ButtonSize, { release: Role; press: Role; lock: Role }> = {
	sm: { release: Roles.ButtonSmReleaseA, press: Roles.ButtonSmPressA, lock: Roles.ButtonSmLockA },
	md: { release: Roles.ButtonMdReleaseA, press: Roles.ButtonMdPressA, lock: Roles.ButtonMdLockA },
	lg: { release: Roles.ButtonLgReleaseA, press: Roles.ButtonLgPressA, lock: Roles.ButtonLgLockA },
};

const buttonDefaultDims: Record<ButtonSize, { w: number; h: number }> = {
	sm: { w: 64, h: 22 },
	md: { w: 96, h: 28 },
	lg: { w: 128, h: 36 },
};

/** Create a theme-skinned FancyButton wrapped in a layout-aware
 *  container. Returns the underlying FancyButton so callers can
 *  attach signals (`onPress.connect`, `onHover.connect`, etc.). */
export function makeButton(opts: ButtonOptions): FancyButton {
	const size = opts.size ?? "md";
	const roles = buttonRoles[size];
	const dims = buttonDefaultDims[size];
	const w = opts.width ?? dims.w;
	const h = opts.height ?? dims.h;

	const btn = new FancyButton({
		defaultView: new NineSlice({ theme: opts.theme, role: roles.release, width: w, height: h }),
		hoverView:   new NineSlice({ theme: opts.theme, role: roles.release, width: w, height: h }),
		pressedView: new NineSlice({ theme: opts.theme, role: roles.press,   width: w, height: h }),
		disabledView: new NineSlice({ theme: opts.theme, role: roles.lock,    width: w, height: h }),
		text: opts.label,
		textOffset: { x: 0, y: -2 },
		animations: {
			hover:   { props: { scale: { x: 1.02, y: 1.02 } }, duration: 80 },
			pressed: { props: { scale: { x: 0.98, y: 0.98 } }, duration: 60 },
		},
	});
	btn.enabled = !opts.disabled;
	btn.layout = { width: w, height: h, alignSelf: "center" };
	if (opts.onPress) {
		btn.onPress.connect(() => { void Promise.resolve(opts.onPress?.()); });
	}
	return btn;
}

// ---- Checkbox -------------------------------------------------------

export interface CheckBoxOptions {
	theme: Theme;
	label: string;
	checked?: boolean;
	onChange?: (checked: boolean) => void;
}

/** Theme-skinned checkbox. Uses the slot_available / slot_selected
 *  sprites for the inactive / active states. The `text` field on
 *  CheckBoxStyle is ignored by `@pixi/ui` v2 — the label flows
 *  through the top-level `text` option instead. */
export function makeCheckBox(opts: CheckBoxOptions): PixiCheckBox {
	const w = 22;
	const h = 22;
	const cb = new PixiCheckBox({
		checked: opts.checked ?? false,
		text: opts.label,
		style: {
			unchecked: new NineSlice({ theme: opts.theme, role: Roles.SlotAvailable, width: w, height: h }),
			checked:   new NineSlice({ theme: opts.theme, role: Roles.SlotSelected,  width: w, height: h }),
			// `text` here is the PixiTextStyle for the label; the
			// label string itself flows through the top-level
			// CheckBoxOptions.text option.
			text: {
				fontFamily: "ui-sans-serif, system-ui, sans-serif",
				fontSize: 12,
				fill: 0xe8ecf2,
			},
		},
	});
	cb.layout = { alignSelf: "center" };
	if (opts.onChange) {
		cb.onCheck.connect((c: boolean) => opts.onChange?.(c));
	}
	return cb;
}

// ---- Slider ---------------------------------------------------------

export interface SliderOptions {
	theme: Theme;
	min: number;
	max: number;
	value?: number;
	step?: number;
	width?: number;
	onChange?: (value: number) => void;
}

export function makeSlider(opts: SliderOptions): PixiSlider {
	const w = opts.width ?? 160;
	const h = 16;
	const sliderOpts: ConstructorParameters<typeof PixiSlider>[0] = {
		bg:   new NineSlice({ theme: opts.theme, role: Roles.SliderBar,    width: w, height: h }),
		fill: new NineSlice({ theme: opts.theme, role: Roles.SliderFiller, width: w, height: h }),
		slider: new NineSlice({ theme: opts.theme, role: Roles.SliderHandle, width: 16, height: 16 }),
		min: opts.min,
		max: opts.max,
		value: opts.value ?? opts.min,
	};
	if (opts.step !== undefined) sliderOpts.step = opts.step;
	const slider = new PixiSlider(sliderOpts);
	slider.layout = { width: w, height: h, alignSelf: "center" };
	if (opts.onChange) {
		slider.onChange.connect((v: number) => opts.onChange?.(v));
	}
	return slider;
}

// ---- Input (text field) ---------------------------------------------

export interface InputOptions {
	theme: Theme;
	value?: string;
	placeholder?: string;
	maxLength?: number;
	width?: number;
	onChange?: (value: string) => void;
	onEnter?: (value: string) => void;
}

/** Theme-skinned text input. Note: `@pixi/ui`'s Input creates one
 *  hidden DOM `<input>` while focused for IME / mobile keyboard /
 *  paste support. All visible chrome is Pixi-rendered.
 *
 *  Async caveat: the underlying texture loads through Theme; until
 *  it resolves, the Input renders against a fallback texture. The
 *  caller can await `theme.textureFor(Roles.Textfield)` first if
 *  they need pixel-perfect first-frame rendering. */
export function makeInput(opts: InputOptions): PixiInput {
	const w = opts.width ?? 200;
	const h = 24;
	const entry = opts.theme.get(Roles.Textfield);
	const insets = entry?.nineSlice ?? { left: 6, top: 6, right: 6, bottom: 6 };
	// `@pixi/ui` Input requires a Texture / string / Sprite / Graphics
	// for `bg` — NineSlice is a Container, not a sprite. We pass the
	// asset URL as a string; @pixi/ui calls Texture.from() under the
	// hood and the texture loads via Pixi Assets.
	const bgRef: string = entry?.assetUrl ?? "";
	const inputCtorOpts: ConstructorParameters<typeof PixiInput>[0] = {
		bg: bgRef.length > 0 ? bgRef : Texture.WHITE,
		nineSliceSprite: [insets.left, insets.top, insets.right, insets.bottom],
		placeholder: opts.placeholder ?? "",
		value: opts.value ?? "",
		padding: { top: 4, right: 8, bottom: 4, left: 8 },
		textStyle: {
			fontFamily: "ui-sans-serif, system-ui, sans-serif",
			fontSize: 13,
			fill: 0xe8ecf2,
		},
	};
	if (opts.maxLength !== undefined) inputCtorOpts.maxLength = opts.maxLength;
	const input = new PixiInput(inputCtorOpts);
	input.layout = { width: w, height: h, alignSelf: "center" };
	if (opts.onChange) input.onChange.connect((v: string) => opts.onChange?.(v));
	if (opts.onEnter) input.onEnter.connect((v: string) => opts.onEnter?.(v));
	return input;
}

// ---- List (vertical stack of items) ---------------------------------

export interface ListOptions {
	theme: Theme;
	items: Container[];
	gap?: number;
}

/** Theme-aware vertical List. Used by sidebars to stack toolbar
 *  buttons + palette entries + layer rows. */
export function makeList(opts: ListOptions): PixiList {
	const list = new PixiList({
		type: "vertical",
		elementsMargin: opts.gap ?? 6,
	});
	for (const item of opts.items) list.addChild(item);
	void opts.theme; // theme reserved for future styling hooks
	return list;
}

// ---- ScrollBox (scrollable list) ------------------------------------

export interface ScrollBoxOptions {
	theme: Theme;
	width: number;
	height: number;
	items: Container[];
	gap?: number;
}

export function makeScrollBox(opts: ScrollBoxOptions): PixiScrollBox {
	const sb = new PixiScrollBox({
		width: opts.width,
		height: opts.height,
		elementsMargin: opts.gap ?? 4,
		items: opts.items,
		type: "vertical",
	});
	sb.layout = { width: opts.width, height: opts.height };
	void opts.theme;
	return sb;
}

// ---- Select (dropdown) ----------------------------------------------

export interface SelectOptions {
	theme: Theme;
	items: string[];
	selected?: number;
	width?: number;
	onChange?: (index: number, value: string) => void;
}

export function makeSelect(opts: SelectOptions): PixiSelect {
	const w = opts.width ?? 160;
	const h = 24;
	const closedBg = new NineSlice({ theme: opts.theme, role: Roles.DropdownBar, width: w, height: h });
	const openBg = new NineSlice({ theme: opts.theme, role: Roles.DropdownBar, width: w, height: h * (opts.items.length + 1) });
	const sel = new PixiSelect({
		closedBG: closedBg as unknown as Container,
		openBG: openBg as unknown as Container,
		textStyle: {
			fontFamily: "ui-sans-serif, system-ui, sans-serif",
			fontSize: 13,
			fill: 0xe8ecf2,
		},
		items: {
			items: opts.items,
			backgroundColor: 0x000000,
			hoverColor: 0xffffff,
			width: w,
			height: h,
			textStyle: {
				fontFamily: "ui-sans-serif, system-ui, sans-serif",
				fontSize: 13,
				fill: 0xe8ecf2,
			},
		},
		selected: opts.selected ?? 0,
	});
	sel.layout = { width: w, height: h, alignSelf: "center" };
	if (opts.onChange) {
		sel.onSelect.connect((index: number, value: string) => opts.onChange?.(index, value));
	}
	return sel;
}

// ---- Re-exports so callers don't always need a factory --------------
export {
	FancyButton,
	PixiCheckBox as CheckBox,
	PixiSlider as Slider,
	PixiInput as Input,
	PixiList as List,
	PixiScrollBox as ScrollBox,
	PixiSelect as Select,
};

// ---- Plain text label ----------------------------------------------

export interface LabelOptions {
	text: string;
	size?: number;
	color?: number;
}

/** Layout-aware Pixi Text. Convenience for UI labels in flex slots. */
export function makeLabel(opts: LabelOptions): Text {
	const t = new Text({
		text: opts.text,
		style: {
			fontFamily: "ui-sans-serif, system-ui, sans-serif",
			fontSize: opts.size ?? 12,
			fill: opts.color ?? 0xe8ecf2,
		},
	});
	t.layout = { alignSelf: "center" };
	return t;
}
