# Mapmaker Tile Rotation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-placed-cell tile rotation that is honored by Mapmaker, persistence, runtime rendering data, collision, colliders, and sockets.

**Architecture:** Store quarter-turn rotation on `map_tiles`, round-trip it through map services and designer JSON, and apply it via shared rotation helpers when materializing runtime components. Mapmaker owns the authoring control and canvas preview.

**Tech Stack:** Go services/tests, PostgreSQL migrations, Templ/static JS Mapmaker, FlatBuffers schema documentation.

---

## Tasks

- [ ] Add failing tests for persistence and loader rotation round-trip.
- [ ] Add migration and Go model/query support for `rotation_degrees`.
- [ ] Add transform helpers for rotating edge masks, collision shapes, colliders, and sockets, with tests.
- [ ] Wire loader/runtime components and schema fields for tile rotation.
- [ ] Wire designer JSON handlers with validation.
- [ ] Add Mapmaker rotate button/hotkey/status and rotated canvas drawing.
- [ ] Run focused tests and broad build/test verification.
