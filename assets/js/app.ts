// If you want to use Phoenix channels, run `mix help phx.gen.channel`
// to get started and then uncomment the line below.
// import "./user_socket.js"

// You can include dependencies in two ways.
//
// The simplest option is to put them in assets/vendor and
// import them using relative paths:
//
//     import "../vendor/some-package.js"
//
// Alternatively, you can `npm install some-package --prefix assets` and import
// them using a path starting with the package name:
//
//     import "some-package"
//
// If you have dependencies that try to import CSS, esbuild will generate a separate `app.css` file.
// To load it, simply add a second `<link>` to your `root.html.heex` file.

// Include phoenix_html to handle method=PUT/DELETE in forms and buttons.
import "phoenix_html"
// Establish Phoenix Socket and LiveView configuration.
import {Socket} from "phoenix"
import {LiveSocket} from "phoenix_live_view"
import {hooks as colocatedHooks} from "phoenix-colocated/boxland"
// @ts-expect-error – topbar has no type declarations
import topbar from "../vendor/topbar"

declare global {
  interface Window {
    liveSocket: typeof liveSocket;
    liveReloader: unknown;
  }
}

const csrfMeta = document.querySelector("meta[name='csrf-token']")
const csrfToken = csrfMeta ? csrfMeta.getAttribute("content") : null

type MapmakerCanvasHook = {
  el: HTMLElement;
  pushEvent(event: string, payload: Record<string, unknown>): void;
  spaceDown: boolean;
  painting: boolean;
  panning: boolean;
  selecting: boolean;
  selectStart: {x: number; y: number} | null;
  lastSelectKey: string | null;
  lastCursorKey: string | null;
  moved: boolean;
  lastCell: string | null;
  panStartX: number;
  panStartY: number;
  scrollStartLeft: number;
  scrollStartTop: number;
  keyDown: (event: KeyboardEvent) => void;
  keyUp: (event: KeyboardEvent) => void;
  pointerDown: (event: PointerEvent) => void;
  pointerMove: (event: PointerEvent) => void;
  pointerUp: (event: PointerEvent) => void;
  click: (event: MouseEvent) => void;
  emitPaint(cell: HTMLElement): void;
  emitSelect(cell: HTMLElement): void;
  cellFromEvent(event: Event): HTMLElement | null;
}

const MapmakerCanvas = {
  mounted(this: MapmakerCanvasHook) {
    this.spaceDown = false
    this.painting = false
    this.panning = false
    this.selecting = false
    this.selectStart = null
    this.lastSelectKey = null
    this.lastCursorKey = null
    this.moved = false
    this.lastCell = null
    this.panStartX = 0
    this.panStartY = 0
    this.scrollStartLeft = 0
    this.scrollStartTop = 0

    this.keyDown = event => {
      if (event.code === "Space") {
        this.spaceDown = true
      }
    }

    this.keyUp = event => {
      if (event.code === "Space") {
        this.spaceDown = false
      }
    }

    this.pointerDown = event => {
      const shouldPan = event.button === 1 || (event.button === 0 && this.spaceDown)

      if (shouldPan) {
        event.preventDefault()
        this.panning = true
        this.moved = false
        this.panStartX = event.clientX
        this.panStartY = event.clientY
        this.scrollStartLeft = this.el.scrollLeft
        this.scrollStartTop = this.el.scrollTop
        this.el.setPointerCapture(event.pointerId)
        return
      }

      const cell = this.cellFromEvent(event)
      if (event.button === 0 && this.el.dataset["tool"] === "place" && cell) {
        event.preventDefault()
        this.painting = true
        this.moved = true
        this.lastCell = null
        this.el.setPointerCapture(event.pointerId)
        this.emitPaint(cell)
        return
      }

      if (event.button === 0 && this.el.dataset["tool"] === "select_area" && cell) {
        event.preventDefault()
        this.selecting = true
        this.moved = true
        this.selectStart = {x: Number(cell.dataset["x"]), y: Number(cell.dataset["y"])}
        this.lastSelectKey = null
        this.el.setPointerCapture(event.pointerId)
        this.emitSelect(cell)
      }
    }

    this.pointerMove = event => {
      if (this.panning) {
        event.preventDefault()
        const dx = event.clientX - this.panStartX
        const dy = event.clientY - this.panStartY
        this.moved = this.moved || Math.abs(dx) > 2 || Math.abs(dy) > 2
        this.el.scrollLeft = this.scrollStartLeft - dx
        this.el.scrollTop = this.scrollStartTop - dy
        return
      }

      if (this.painting) {
        const cell = this.cellFromEvent(event)
        if (cell) {
          event.preventDefault()
          this.emitPaint(cell)
        }
        return
      }

      if (this.selecting) {
        const cell = this.cellFromEvent(event)
        if (cell) {
          event.preventDefault()
          this.emitSelect(cell)
        }
        return
      }

      const tool = this.el.dataset["tool"]
      if (tool === "move" || tool === "clone") {
        const cell = this.cellFromEvent(event)
        if (!cell) return
        const x = cell.dataset["x"]
        const y = cell.dataset["y"]
        if (!x || !y) return
        const key = `${x},${y}`
        if (key !== this.lastCursorKey) {
          this.lastCursorKey = key
          this.pushEvent("cursor_at", {x, y})
        }
      }
    }

    this.pointerUp = event => {
      if (this.panning || this.painting || this.selecting) {
        event.preventDefault()
        if (this.el.hasPointerCapture(event.pointerId)) {
          this.el.releasePointerCapture(event.pointerId)
        }
      }

      this.panning = false
      this.painting = false
      this.selecting = false
      this.selectStart = null
      this.lastSelectKey = null
      this.lastCursorKey = null
      this.lastCell = null
    }

    this.click = event => {
      if (this.moved) {
        event.preventDefault()
        event.stopImmediatePropagation()
        this.moved = false
      }
    }

    this.emitPaint = cell => {
      const x = cell.dataset["x"]
      const y = cell.dataset["y"]
      if (!x || !y) {
        return
      }

      const key = `${x},${y}`
      if (key === this.lastCell) {
        return
      }

      this.lastCell = key
      this.pushEvent("paint_cell", {x, y})
    }

    this.emitSelect = cell => {
      if (!this.selectStart) return

      const x = Number(cell.dataset["x"])
      const y = Number(cell.dataset["y"])
      if (Number.isNaN(x) || Number.isNaN(y)) return

      const key = `${x},${y}`
      if (key === this.lastSelectKey) return
      this.lastSelectKey = key

      const x1 = Math.min(this.selectStart.x, x)
      const y1 = Math.min(this.selectStart.y, y)
      const x2 = Math.max(this.selectStart.x, x)
      const y2 = Math.max(this.selectStart.y, y)
      this.pushEvent("select_area_drag", {x1, y1, x2, y2})
    }

    this.cellFromEvent = event => {
      const pointerEvent = event instanceof PointerEvent ? event : null
      const target = pointerEvent
        ? document.elementFromPoint(pointerEvent.clientX, pointerEvent.clientY)
        : event.target

      return target instanceof Element ? target.closest<HTMLElement>("[data-map-cell]") : null
    }

    window.addEventListener("keydown", this.keyDown)
    window.addEventListener("keyup", this.keyUp)
    this.el.addEventListener("pointerdown", this.pointerDown)
    this.el.addEventListener("pointermove", this.pointerMove)
    this.el.addEventListener("pointerup", this.pointerUp)
    this.el.addEventListener("pointercancel", this.pointerUp)
    this.el.addEventListener("click", this.click, true)
  },

  destroyed(this: MapmakerCanvasHook) {
    window.removeEventListener("keydown", this.keyDown)
    window.removeEventListener("keyup", this.keyUp)
    this.el.removeEventListener("pointerdown", this.pointerDown)
    this.el.removeEventListener("pointermove", this.pointerMove)
    this.el.removeEventListener("pointerup", this.pointerUp)
    this.el.removeEventListener("pointercancel", this.pointerUp)
    this.el.removeEventListener("click", this.click, true)
  },
}

type TileMaskPainterHook = {
  el: HTMLElement;
  pushEvent(event: string, payload: Record<string, unknown>): void;
  painting: boolean;
  paintValue: boolean;
  seen: Set<string>;
  pending: Array<[number, number]>;
  flushTimer: number | null;
  pointerDown: (event: PointerEvent) => void;
  pointerMove: (event: PointerEvent) => void;
  pointerUp: (event: PointerEvent) => void;
  cellFromEvent(event: PointerEvent): HTMLElement | null;
  touchCell(cell: HTMLElement): void;
  scheduleFlush(): void;
  flush(): void;
}

const TileMaskPainter = {
  mounted(this: TileMaskPainterHook) {
    this.painting = false
    this.paintValue = true
    this.seen = new Set<string>()
    this.pending = []
    this.flushTimer = null

    this.pointerDown = event => {
      if (event.button !== 0) return
      const cell = this.cellFromEvent(event)
      if (!cell) return

      event.preventDefault()
      this.painting = true
      this.paintValue = cell.dataset["solid"] !== "1"
      this.seen.clear()
      this.pending = []
      this.el.setPointerCapture(event.pointerId)
      this.touchCell(cell)
    }

    this.pointerMove = event => {
      if (!this.painting) return
      const cell = this.cellFromEvent(event)
      if (cell) {
        event.preventDefault()
        this.touchCell(cell)
      }
    }

    this.pointerUp = event => {
      if (!this.painting) return
      event.preventDefault()
      if (this.el.hasPointerCapture(event.pointerId)) {
        this.el.releasePointerCapture(event.pointerId)
      }
      this.painting = false
      this.flush()
    }

    this.cellFromEvent = event => {
      const target = document.elementFromPoint(event.clientX, event.clientY)
      if (!(target instanceof Element)) return null
      const cell = target.closest<HTMLElement>("[data-mask-cell]")
      if (!cell || !this.el.contains(cell)) return null
      return cell
    }

    this.touchCell = cell => {
      const x = cell.dataset["x"]
      const y = cell.dataset["y"]
      if (x === undefined || y === undefined) return
      const key = `${x},${y}`
      if (this.seen.has(key)) return
      this.seen.add(key)

      cell.dataset["solid"] = this.paintValue ? "1" : "0"
      cell.classList.toggle("bg-error/55", this.paintValue)
      cell.classList.toggle("bg-transparent", !this.paintValue)

      this.pending.push([parseInt(x, 10), parseInt(y, 10)])
      this.scheduleFlush()
    }

    this.scheduleFlush = () => {
      if (this.flushTimer !== null) return
      this.flushTimer = window.setTimeout(() => {
        this.flushTimer = null
        this.flush()
      }, 60)
    }

    this.flush = () => {
      if (this.flushTimer !== null) {
        window.clearTimeout(this.flushTimer)
        this.flushTimer = null
      }
      if (this.pending.length === 0) return
      const pixels = this.pending
      this.pending = []
      this.pushEvent("paint_pixels", {pixels, value: this.paintValue})
    }

    this.el.addEventListener("pointerdown", this.pointerDown)
    this.el.addEventListener("pointermove", this.pointerMove)
    this.el.addEventListener("pointerup", this.pointerUp)
    this.el.addEventListener("pointercancel", this.pointerUp)
  },

  destroyed(this: TileMaskPainterHook) {
    if (this.flushTimer !== null) {
      window.clearTimeout(this.flushTimer)
      this.flushTimer = null
    }
    this.el.removeEventListener("pointerdown", this.pointerDown)
    this.el.removeEventListener("pointermove", this.pointerMove)
    this.el.removeEventListener("pointerup", this.pointerUp)
    this.el.removeEventListener("pointercancel", this.pointerUp)
  },
}

// === Sprite animation hook =========================================
//
// Plays a named spritesheet animation by stepping `background-position`
// through a 32px frame grid. All playback state lives client-side; the
// server only renders `data-sprite-*` attributes:
//
//   data-sprite-url     spritesheet image URL
//   data-sprite-cols    frames per row in the sheet
//   data-sprite-rows    rows in the sheet
//   data-sprite-tile    rendered frame size in px (32, or larger for zoomed previews)
//   data-sprite-frames  comma-separated frame indexes in playback order
//   data-sprite-fps     frames per second (ambient mode)
//   data-sprite-loop    "true" | "false"
//   data-sprite-sync    "ambient" (free-running, shared rAF ticker) or
//                       "tick" (frame is a pure function of data-sprite-tick —
//                       deterministic under the play-mode scrubber)
//   data-sprite-tick    current sim tick (tick mode only)
//   data-sprite-ticks-per-frame  sim ticks per animation frame (tick mode, default 2)
//
// One module-level rAF loop drives every ambient sprite (per-element fps via
// ms accumulators) instead of per-element timers.

type SpriteRecord = {
  el: HTMLElement;
  url: string;
  cols: number;
  rows: number;
  tile: number;
  frames: number[];
  fps: number;
  loop: boolean;
  accMs: number;
  index: number;
  lastFrame: number | null;
}

const spriteRegistry = new Set<SpriteRecord>()
let spriteRafId: number | null = null
let spriteLastTs: number | null = null

function spriteLoop(ts: number) {
  const dt = spriteLastTs === null ? 0 : ts - spriteLastTs
  spriteLastTs = ts

  spriteRegistry.forEach(rec => {
    if (rec.frames.length === 0) return
    rec.accMs += dt
    const frameMs = 1000 / rec.fps
    while (rec.accMs >= frameMs) {
      rec.accMs -= frameMs
      if (rec.loop) {
        rec.index = (rec.index + 1) % rec.frames.length
      } else if (rec.index < rec.frames.length - 1) {
        rec.index += 1
      }
    }
    spritePaint(rec, rec.frames[Math.min(rec.index, rec.frames.length - 1)])
  })

  if (spriteRegistry.size > 0) {
    spriteRafId = requestAnimationFrame(spriteLoop)
  } else {
    spriteRafId = null
    spriteLastTs = null
  }
}

function spriteEnsureLoop() {
  if (spriteRafId === null && spriteRegistry.size > 0) {
    spriteLastTs = null
    spriteRafId = requestAnimationFrame(spriteLoop)
  }
}

function spritePaint(rec: SpriteRecord, frame: number) {
  if (rec.lastFrame === frame) return
  rec.lastFrame = frame
  const col = frame % rec.cols
  const row = Math.floor(frame / rec.cols)
  rec.el.style.backgroundPosition = `-${col * rec.tile}px -${row * rec.tile}px`
}

// LiveView owns the `style` attribute and may re-render it on any patch, so
// the image/size/position must be re-asserted from JS after every update.
function spriteAssertBase(rec: SpriteRecord) {
  rec.el.style.backgroundImage = `url('${rec.url}')`
  rec.el.style.backgroundSize = `${rec.cols * rec.tile}px ${rec.rows * rec.tile}px`
  rec.el.style.backgroundRepeat = "no-repeat"
  rec.lastFrame = null
}

type SpriteHook = {
  el: HTMLElement;
  rec: SpriteRecord | null;
  signature: string;
  sync: () => void;
}

const Sprite = {
  mounted(this: SpriteHook) {
    this.rec = null
    this.signature = ""
    this.sync()
  },

  updated(this: SpriteHook) {
    this.sync()
  },

  destroyed(this: SpriteHook) {
    if (this.rec) spriteRegistry.delete(this.rec)
    this.rec = null
  },

  sync(this: SpriteHook) {
    const d = this.el.dataset
    const frames = (d["spriteFrames"] ?? "")
      .split(",")
      .map(s => parseInt(s, 10))
      .filter(n => Number.isFinite(n) && n >= 0)

    const rec: SpriteRecord = {
      el: this.el,
      url: d["spriteUrl"] ?? "",
      cols: Math.max(1, parseInt(d["spriteCols"] ?? "1", 10) || 1),
      rows: Math.max(1, parseInt(d["spriteRows"] ?? "1", 10) || 1),
      tile: Math.max(1, parseInt(d["spriteTile"] ?? "32", 10) || 32),
      frames,
      fps: Math.max(1, parseInt(d["spriteFps"] ?? "8", 10) || 8),
      loop: d["spriteLoop"] !== "false",
      accMs: this.rec?.accMs ?? 0,
      index: this.rec?.index ?? 0,
      lastFrame: null,
    }

    // Restart playback when the animation itself changed (different sheet
    // or frame list, e.g. an idle→moving binding switch).
    const signature = `${rec.url}|${rec.frames.join(",")}|${d["spriteSync"] ?? "ambient"}`
    if (signature !== this.signature) {
      this.signature = signature
      rec.accMs = 0
      rec.index = 0
    }

    if (this.rec) spriteRegistry.delete(this.rec)
    this.rec = rec
    spriteAssertBase(rec)
    if (rec.frames.length === 0) return

    if ((d["spriteSync"] ?? "ambient") === "tick") {
      // Deterministic: frame is a pure function of the sim tick, so
      // scrubbing the timeline always reproduces the same frame.
      const tick = parseInt(d["spriteTick"] ?? "0", 10) || 0
      const perFrame = Math.max(1, parseInt(d["spriteTicksPerFrame"] ?? "2", 10) || 2)
      const step = Math.floor(tick / perFrame)
      const idx = rec.loop ? step % rec.frames.length : Math.min(step, rec.frames.length - 1)
      spritePaint(rec, rec.frames[idx])
    } else {
      spritePaint(rec, rec.frames[Math.min(rec.index, rec.frames.length - 1)])
      spriteRegistry.add(rec)
      spriteEnsureLoop()
    }
  },
}

// === IDE shell hooks ===============================================

// Right-click context menus. Place on a wrapper; any descendant carrying
// [data-context-menu] becomes a target. Pushes coords so the server renders
// <.context_menu> at the cursor.
type ContextMenuHook = {
  el: HTMLElement;
  pushEvent(event: string, payload: Record<string, unknown>): void;
  onContext: (event: MouseEvent) => void;
}

const ContextMenu = {
  mounted(this: ContextMenuHook) {
    this.onContext = event => {
      const target = event.target instanceof Element ? event.target : null
      const node = target?.closest<HTMLElement>("[data-context-menu]")
      if (!node || !this.el.contains(node)) return
      event.preventDefault()
      this.pushEvent("open_context_menu", {
        kind: node.dataset["contextMenu"] ?? null,
        id: node.dataset["contextId"] ?? null,
        x: event.clientX,
        y: event.clientY,
      })
    }
    this.el.addEventListener("contextmenu", this.onContext)
  },

  destroyed(this: ContextMenuHook) {
    this.el.removeEventListener("contextmenu", this.onContext)
  },
}

// Drag-to-reorder tree nodes. Place on the <ul role="tree" data-tree-group>.
// Each reorderable child carries [data-tree-item="<id>"]. On drop, pushes
// {group, id, before_id} (before_id null = move to end).
type TreeDnDHook = {
  el: HTMLElement;
  pushEvent(event: string, payload: Record<string, unknown>): void;
  dragId: string | null;
  overEl: HTMLElement | null;
  before: boolean;
  enableDrag: () => void;
  clearMarks: () => void;
  onDragStart: (event: DragEvent) => void;
  onDragOver: (event: DragEvent) => void;
  onDrop: (event: DragEvent) => void;
  onDragEnd: () => void;
}

const TreeDnD = {
  mounted(this: TreeDnDHook) {
    this.dragId = null
    this.overEl = null
    this.before = true

    this.enableDrag = () => {
      this.el.querySelectorAll<HTMLElement>("[data-tree-item]").forEach(li => {
        li.draggable = true
      })
    }

    this.clearMarks = () => {
      this.el.querySelectorAll(".ide-drop-before, .ide-drop-after").forEach(n =>
        n.classList.remove("ide-drop-before", "ide-drop-after"),
      )
    }

    this.onDragStart = event => {
      const target = event.target instanceof Element ? event.target : null
      const li = target?.closest<HTMLElement>("[data-tree-item]")
      if (!li) return
      this.dragId = li.dataset["treeItem"] ?? null
      li.classList.add("ide-node-dragging")
      if (event.dataTransfer) {
        event.dataTransfer.effectAllowed = "move"
        event.dataTransfer.setData("text/plain", this.dragId ?? "")
      }
    }

    this.onDragOver = event => {
      if (this.dragId === null) return
      const target = event.target instanceof Element ? event.target : null
      const li = target?.closest<HTMLElement>("[data-tree-item]")
      if (!li) return
      event.preventDefault()
      const rect = li.getBoundingClientRect()
      const before = event.clientY < rect.top + rect.height / 2
      if (this.overEl !== li || this.before !== before) {
        this.clearMarks()
        this.overEl = li
        this.before = before
        li.classList.add(before ? "ide-drop-before" : "ide-drop-after")
      }
    }

    this.onDrop = event => {
      if (this.dragId === null || !this.overEl) return
      event.preventDefault()
      let beforeId: string | null = this.overEl.dataset["treeItem"] ?? null
      if (!this.before) {
        const next = this.overEl.nextElementSibling
        beforeId = next instanceof HTMLElement ? next.dataset["treeItem"] ?? null : null
      }
      if (this.dragId !== beforeId) {
        this.pushEvent("tree_reorder", {
          group: this.el.dataset["treeGroup"] ?? null,
          id: this.dragId,
          before_id: beforeId,
        })
      }
      this.onDragEnd()
    }

    this.onDragEnd = () => {
      this.clearMarks()
      this.el
        .querySelectorAll(".ide-node-dragging")
        .forEach(n => n.classList.remove("ide-node-dragging"))
      this.dragId = null
      this.overEl = null
    }

    this.enableDrag()
    this.el.addEventListener("dragstart", this.onDragStart)
    this.el.addEventListener("dragover", this.onDragOver)
    this.el.addEventListener("drop", this.onDrop)
    this.el.addEventListener("dragend", this.onDragEnd)
  },

  updated(this: TreeDnDHook) {
    this.enableDrag()
  },

  destroyed(this: TreeDnDHook) {
    this.el.removeEventListener("dragstart", this.onDragStart)
    this.el.removeEventListener("dragover", this.onDragOver)
    this.el.removeEventListener("drop", this.onDrop)
    this.el.removeEventListener("dragend", this.onDragEnd)
  },
}

// Drag numbered waypoint markers on the canvas. Place on the canvas wrapper.
// Markers carry [data-waypoint-index]; cells carry [data-cell-x]/[data-cell-y].
// Drop on a cell -> waypoint_move; drop off-grid -> waypoint_remove.
type WaypointDragHook = {
  el: HTMLElement;
  pushEvent(event: string, payload: Record<string, unknown>): void;
  dragging: HTMLElement | null;
  index: number | null;
  onDown: (event: PointerEvent) => void;
  onMove: (event: PointerEvent) => void;
  onUp: (event: PointerEvent) => void;
  cellAt: (x: number, y: number) => {x: number; y: number} | null;
}

const WaypointDrag = {
  mounted(this: WaypointDragHook) {
    this.dragging = null
    this.index = null

    this.onDown = event => {
      if (event.button !== 0) return
      const target = event.target instanceof Element ? event.target : null
      const marker = target?.closest<HTMLElement>("[data-waypoint-index]")
      if (!marker) return
      event.preventDefault()
      event.stopPropagation()
      this.dragging = marker
      this.index = Number(marker.dataset["waypointIndex"])
      marker.setPointerCapture(event.pointerId)
      marker.classList.add("opacity-60")
    }

    this.onMove = event => {
      if (this.dragging) event.preventDefault()
    }

    this.onUp = event => {
      if (!this.dragging || this.index === null) return
      event.preventDefault()
      this.dragging.classList.remove("opacity-60")
      const cell = this.cellAt(event.clientX, event.clientY)
      if (cell) {
        this.pushEvent("waypoint_move", {index: this.index, x: cell.x, y: cell.y})
      } else {
        this.pushEvent("waypoint_remove", {index: this.index})
      }
      this.dragging = null
      this.index = null
    }

    this.cellAt = (x, y) => {
      const cell = document
        .elementsFromPoint(x, y)
        .find((node): node is HTMLElement => node instanceof HTMLElement && node.dataset["cellX"] !== undefined)
      if (!cell) return null
      return {x: Number(cell.dataset["cellX"]), y: Number(cell.dataset["cellY"])}
    }

    this.el.addEventListener("pointerdown", this.onDown)
    this.el.addEventListener("pointermove", this.onMove)
    this.el.addEventListener("pointerup", this.onUp)
    this.el.addEventListener("pointercancel", this.onUp)
  },

  destroyed(this: WaypointDragHook) {
    this.el.removeEventListener("pointerdown", this.onDown)
    this.el.removeEventListener("pointermove", this.onMove)
    this.el.removeEventListener("pointerup", this.onUp)
    this.el.removeEventListener("pointercancel", this.onUp)
  },
}

const liveSocket = new LiveSocket("/live", Socket, {
  longPollFallbackMs: 2500,
  params: {_csrf_token: csrfToken},
  hooks: {...colocatedHooks, MapmakerCanvas, TileMaskPainter, ContextMenu, TreeDnD, WaypointDrag, Sprite},
})

// Show progress bar on live navigation and form submits
// eslint-disable-next-line @typescript-eslint/no-unsafe-call, @typescript-eslint/no-unsafe-member-access
topbar.config({barColors: {0: "#29d"}, shadowColor: "rgba(0, 0, 0, .3)"})
// eslint-disable-next-line @typescript-eslint/no-unsafe-member-access
window.addEventListener("phx:page-loading-start", _info => topbar.show(300))
// eslint-disable-next-line @typescript-eslint/no-unsafe-member-access
window.addEventListener("phx:page-loading-stop", _info => topbar.hide())

// connect if there are any LiveViews on the page
liveSocket.connect()

// expose liveSocket on window for web console debug logs and latency simulation:
// >> liveSocket.enableDebug()
// >> liveSocket.enableLatencySim(1000)  // enabled for duration of browser session
// >> liveSocket.disableLatencySim()
window.liveSocket = liveSocket

// The lines below enable quality of life phoenix_live_reload
// development features:
//
//     1. stream server logs to the browser console
//     2. click on elements to jump to their definitions in your code editor
//
if (typeof process !== "undefined" && process.env["NODE_ENV"] === "development") {
  window.addEventListener("phx:live_reload:attached", (e: Event) => {
    // Enable server log streaming to client.
    // Disable with reloader.disableServerLogs()
    const reloader = (e as CustomEvent<{enableServerLogs(): void; disableServerLogs(): void; openEditorAtCaller(el: EventTarget | null): void; openEditorAtDef(el: EventTarget | null): void}>).detail
    reloader.enableServerLogs()

    // Open configured PLUG_EDITOR at file:line of the clicked element's HEEx component
    //
    //   * click with "c" key pressed to open at caller location
    //   * click with "d" key pressed to open at function component definition location
    let keyDown: string | null = null
    window.addEventListener("keydown", e => { keyDown = e.key })
    window.addEventListener("keyup", _e => { keyDown = null })
    window.addEventListener("click", e => {
      if(keyDown === "c"){
        e.preventDefault()
        e.stopImmediatePropagation()
        reloader.openEditorAtCaller(e.target)
      } else if(keyDown === "d"){
        e.preventDefault()
        e.stopImmediatePropagation()
        reloader.openEditorAtDef(e.target)
      }
    }, true)

    window.liveReloader = reloader
  })
}
