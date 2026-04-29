// Boxland — renderer UI primitives public surface.
//
// Built on top of `@pixi/ui`, `@pixi/layout`, and `pixi-filters`.
// Editors compose these into full surfaces; runtime HUDs reuse the
// same widget set.

export { Theme, Roles, bindThemeToTextureCache } from "./theme";
export type { Role, ThemeEntry, NineSliceInsets } from "./theme";
export { NineSlice } from "./nine-slice";
export type { NineSliceOptions } from "./nine-slice";
export {
	makeButton, makeCheckBox, makeSlider, makeInput,
	makeList, makeScrollBox, makeSelect, makeLabel,
	FancyButton, CheckBox, Slider, Input, List, ScrollBox, Select,
} from "./widgets";
export type {
	ButtonOptions, ButtonSize, CheckBoxOptions, SliderOptions,
	InputOptions, ListOptions, ScrollBoxOptions, SelectOptions,
	LabelOptions,
} from "./widgets";
