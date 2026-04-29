// Boxland — TS mirror of `server/internal/configurable/configurable.go`.
//
// The server hands these descriptors down to clients in editor
// snapshots so the UI can render generic property forms without
// per-type bespoke templates. Adding a new field kind here means
// adding a matching renderer branch in inspector.ts.

export type FieldKind =
	| "string"
	| "text"        // multiline string
	| "int"
	| "float"
	| "bool"
	| "enum"
	| "asset_ref"
	| "entity_type_ref"
	| "color"       // 0xRRGGBBAA
	| "vec2"        // {x, y}
	| "range"       // {min, max}
	| "nested"
	| "list";

export interface EnumOption {
	value: string;
	label: string;
}

export interface FieldDescriptor {
	key: string;
	label: string;
	help?: string;
	kind: FieldKind;
	required?: boolean;
	default?: unknown;

	// Numeric constraints.
	min?: number;
	max?: number;
	step?: number;

	// String constraints.
	max_len?: number;
	pattern?: string;

	// Enum options.
	options?: EnumOption[];

	// Reference filters.
	ref_tags?: string[];

	// Recursion.
	children?: FieldDescriptor[];
}
