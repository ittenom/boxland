// Boxland — character-generator package barrel.
//
// Re-exports the headless reducer + preview class so other modules
// (e.g. a future player-mode entry) can import without reaching into
// internal files.

export * from "./state";
export * from "./preview";
