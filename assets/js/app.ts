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
  pushEvent(event: string, payload: Record<string, string>): void;
  spaceDown: boolean;
  painting: boolean;
  panning: boolean;
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
  cellFromEvent(event: Event): HTMLElement | null;
}

const MapmakerCanvas = {
  mounted(this: MapmakerCanvasHook) {
    this.spaceDown = false
    this.painting = false
    this.panning = false
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
      }
    }

    this.pointerUp = event => {
      if (this.panning || this.painting) {
        event.preventDefault()
        if (this.el.hasPointerCapture(event.pointerId)) {
          this.el.releasePointerCapture(event.pointerId)
        }
      }

      this.panning = false
      this.painting = false
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

const liveSocket = new LiveSocket("/live", Socket, {
  longPollFallbackMs: 2500,
  params: {_csrf_token: csrfToken},
  hooks: {...colocatedHooks, MapmakerCanvas},
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
