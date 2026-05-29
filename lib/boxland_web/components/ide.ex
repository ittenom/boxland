defmodule BoxlandWeb.Components.Ide do
  @moduledoc """
  Shared IDE shell + modular panel components used across the Asset Manager,
  Mapmaker, and Level Editor so they read as one application rather than three.

  The shell is a full-viewport dark chrome:

      ┌──┬──────────┬───────────────┬─────────┐
      │  │ EXPLORER │   VIEWPORT    │ INSPECT │   activity rail · explorer ·
      │R │  (tree)  │   (canvas)    │ (props) │   viewport · inspector
      ├──┴──────────┴───────────────┴─────────┤
      │ status bar                            │
      └───────────────────────────────────────┘

  Components are slot-driven and stateless; host LiveViews own selection,
  collapse state, drag/context-menu events. Visual styling lives in the
  `.ide-*` rules in `assets/css/app.css`.

  Import per-LiveView (`import BoxlandWeb.Components.Ide`) rather than globally,
  so the editors' private `tool_button/1` doesn't clash (the shared toolbar is
  `ide_tool_button/1`).
  """
  use BoxlandWeb, :html

  @doc """
  The full IDE chrome. Slots fill the rail, explorer panel, viewport, inspector
  panel, and status bar. Omitted side panels collapse to zero width.
  """
  attr :flash, :map, required: true
  slot :activity, doc: "left icon rail content (rail_item/1 + logo)"
  slot :explorer, doc: "left docked panel (object tree)"
  slot :viewport, required: true, doc: "center canvas/workspace"
  slot :inspector, doc: "right docked panel (property inspector)"
  slot :status, doc: "bottom status bar content"

  def ide_shell(assigns) do
    ~H"""
    <div class="ide-shell flex h-screen flex-col overflow-hidden" data-theme="dark">
      <div class="flex min-h-0 flex-1">
        <nav class="ide-rail flex w-14 shrink-0 flex-col items-center gap-1 py-2" aria-label="Apps">
          {render_slot(@activity)}
        </nav>

        <aside
          :if={@explorer != []}
          class="ide-panel ide-explorer flex w-64 shrink-0 flex-col overflow-hidden border-r"
          aria-label="Explorer"
        >
          {render_slot(@explorer)}
        </aside>

        <main class="ide-viewport relative flex min-w-0 flex-1 flex-col overflow-hidden">
          {render_slot(@viewport)}
        </main>

        <aside
          :if={@inspector != []}
          class="ide-panel ide-inspector flex w-80 shrink-0 flex-col overflow-y-auto border-l"
          aria-label="Inspector"
        >
          {render_slot(@inspector)}
        </aside>
      </div>

      <footer class="ide-status flex h-7 shrink-0 items-center gap-3 border-t px-3 text-xs">
        {render_slot(@status)}
      </footer>

      <Layouts.flash_group flash={@flash} />
    </div>
    """
  end

  @doc "The standard app-navigation rail items (Workspace / Assets / Maps / Levels)."
  attr :active, :atom, required: true, doc: ":assets | :maps | :levels | :workspace"

  def ide_rail_nav(assigns) do
    ~H"""
    <.link navigate={~p"/app"} class="ide-rail-item" title="Workspace" aria-label="Workspace">
      <.icon name="hero-home" class="size-5" />
    </.link>
    <.rail_item
      icon="hero-photo"
      label="Assets"
      navigate={~p"/app/assets"}
      active={@active == :assets}
    />
    <.rail_item icon="hero-map" label="Maps" navigate={~p"/app/maps"} active={@active == :maps} />
    <.rail_item
      icon="hero-cube"
      label="Levels"
      navigate={~p"/app/levels"}
      active={@active == :levels}
    />
    """
  end

  @doc "A single icon in the activity rail. `navigate` makes it a link; otherwise a button."
  attr :icon, :string, required: true
  attr :label, :string, required: true
  attr :navigate, :string, default: nil
  attr :active, :boolean, default: false
  attr :rest, :global, include: ~w(phx-click phx-value-context phx-value-id)

  def rail_item(assigns) do
    ~H"""
    <%= if @navigate do %>
      <.link
        navigate={@navigate}
        class={["ide-rail-item", @active && "ide-rail-item-active"]}
        title={@label}
        aria-label={@label}
        aria-current={@active && "page"}
      >
        <.icon name={@icon} class="size-5" />
      </.link>
    <% else %>
      <button
        type="button"
        class={["ide-rail-item", @active && "ide-rail-item-active"]}
        title={@label}
        aria-label={@label}
        {@rest}
      >
        <.icon name={@icon} class="size-5" />
      </button>
    <% end %>
    """
  end

  @doc "A docked panel with a sticky title bar and a scrollable body."
  attr :title, :string, required: true
  attr :class, :string, default: nil
  slot :actions, doc: "right-aligned header controls"
  slot :inner_block, required: true

  def panel(assigns) do
    ~H"""
    <div class={["flex min-h-0 flex-col", @class]}>
      <div class="ide-panel-title flex items-center gap-2 px-3 py-2">
        <span class="flex-1 truncate">{@title}</span>
        <span :if={@actions != []} class="flex items-center gap-1">{render_slot(@actions)}</span>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">{render_slot(@inner_block)}</div>
    </div>
    """
  end

  @doc """
  A collapsible titled section. `open` controls visibility; the host wires the
  toggle via the global attrs (e.g. `phx-click="toggle_section" phx-value-id=...`).
  """
  attr :title, :string, required: true
  attr :open, :boolean, default: true
  attr :rest, :global, include: ~w(phx-click phx-value-id phx-value-section)
  slot :actions
  slot :inner_block, required: true

  def panel_section(assigns) do
    ~H"""
    <section class="ide-section">
      <div class="ide-section-header flex items-center">
        <button type="button" class="ide-section-toggle flex flex-1 items-center gap-1.5" {@rest}>
          <.icon name={(@open && "hero-chevron-down") || "hero-chevron-right"} class="size-3" />
          <span class="flex-1 text-left">{@title}</span>
        </button>
        <span :if={@actions != []} class="flex items-center gap-1 pr-1">{render_slot(@actions)}</span>
      </div>
      <div :if={@open} class="ide-section-body">{render_slot(@inner_block)}</div>
    </section>
    """
  end

  @doc "Object tree container (`<ul role=\"tree\">`)."
  attr :id, :string, default: nil
  attr :class, :string, default: nil
  attr :rest, :global, include: ~w(phx-hook data-tree-group)
  slot :inner_block, required: true

  def tree(assigns) do
    ~H"""
    <ul id={@id} role="tree" class={["ide-tree", @class]} {@rest}>
      {render_slot(@inner_block)}
    </ul>
    """
  end

  @doc """
  One tree row. The label area is the selection target (wire `phx-click` via
  `@rest`). `draggable` adds a drag handle for the `TreeDnD` hook; `context_kind`
  + `context_id` mark it as a right-click target for the `ContextMenu` hook.
  """
  attr :id, :string, required: true
  attr :label, :string, default: nil
  attr :icon, :string, default: nil
  attr :depth, :integer, default: 0
  attr :selected, :boolean, default: false

  attr :affected, :boolean,
    default: false,
    doc: "secondary highlight (e.g. layers touched by the selection)"

  attr :draggable, :boolean, default: false
  attr :dnd_id, :any, default: nil, doc: "stable id used in tree_reorder payloads"
  attr :context_kind, :string, default: nil
  attr :context_id, :any, default: nil
  attr :rest, :global, include: ~w(phx-click phx-value-id phx-value-kind)
  slot :inner_block, doc: "custom label content (overrides label attr)"
  slot :trailing, doc: "right-aligned inline controls (visibility, lock, …)"

  def tree_node(assigns) do
    ~H"""
    <li
      id={@id}
      role="treeitem"
      aria-selected={to_string(@selected)}
      data-tree-item={@draggable && to_string(@dnd_id)}
      data-context-menu={@context_kind}
      data-context-id={@context_id && to_string(@context_id)}
      class={["ide-node group", @selected && "ide-node-selected", @affected && "ide-node-affected"]}
      style={"padding-left: #{0.25 + @depth * 0.75}rem"}
    >
      <span
        :if={@draggable}
        data-drag-handle
        class="ide-node-handle"
        aria-hidden="true"
        title="Drag to reorder"
      >
        <.icon name="hero-bars-2" class="size-3" />
      </span>
      <button type="button" class="ide-node-main flex min-w-0 flex-1 items-center gap-1.5" {@rest}>
        <.icon :if={@icon} name={@icon} class="size-3.5 shrink-0 opacity-70" />
        <span class="min-w-0 flex-1 truncate text-left">{@label}{render_slot(@inner_block)}</span>
      </button>
      <span :if={@trailing != []} class="ide-node-trailing flex items-center gap-0.5 pr-1">
        {render_slot(@trailing)}
      </span>
    </li>
    """
  end

  @doc """
  Inspector for a map `Layer`. Wires the standard layer events the editors
  already handle (`rename_layer`, `toggle_visibility`, `toggle_lock`,
  `set_opacity`). Pass `nil` to show an empty state.
  """
  attr :layer, :any, default: nil

  def layer_inspector(assigns) do
    ~H"""
    <.panel title="Layer">
      <div :if={is_nil(@layer)} class="px-2 py-2 text-xs text-base-content/40">
        No layer selected.
      </div>
      <div :if={@layer} id={"layer-inspector-#{@layer.id}"} class="space-y-2 px-2 py-2">
        <.property_row label="Name">
          <form phx-submit="rename_layer" phx-value-id={@layer.id}>
            <input
              type="text"
              name="name"
              value={@layer.name}
              class="input input-xs input-bordered w-full"
            />
          </form>
        </.property_row>
        <.property_row label="Visible">
          <button phx-click="toggle_visibility" phx-value-id={@layer.id} class="ide-toolbtn">
            <.icon name={if @layer.visible, do: "hero-eye", else: "hero-eye-slash"} class="size-4" />
          </button>
        </.property_row>
        <.property_row label="Locked">
          <button phx-click="toggle_lock" phx-value-id={@layer.id} class="ide-toolbtn">
            <.icon
              name={if @layer.locked, do: "hero-lock-closed", else: "hero-lock-open"}
              class="size-4"
            />
          </button>
        </.property_row>
        <.property_row label="Opacity">
          <form phx-change="set_opacity" phx-value-id={@layer.id} class="flex items-center gap-2">
            <input
              type="range"
              min="0"
              max="100"
              step="5"
              value={@layer.opacity}
              name="opacity"
              phx-debounce="150"
              class="range range-xs flex-1"
            />
            <span class="w-9 text-right font-mono text-[10px]">{@layer.opacity}%</span>
          </form>
        </.property_row>
        <.property_row label="z-index">
          <span class="font-mono text-xs">{@layer.z_index}</span>
        </.property_row>
      </div>
    </.panel>
    """
  end

  @doc "A label + control row for the inspector property grid."
  attr :label, :string, required: true
  attr :class, :string, default: nil
  slot :inner_block, required: true

  def property_row(assigns) do
    ~H"""
    <label class={["ide-prop-row flex items-center gap-2", @class]}>
      <span class="ide-prop-label shrink-0">{@label}</span>
      <span class="ide-prop-control min-w-0 flex-1">{render_slot(@inner_block)}</span>
    </label>
    """
  end

  @doc """
  A floating menu positioned at `x`/`y` (set by the `ContextMenu` hook). Renders
  only when `open`. Closes on click-away or Escape via `close_event`.
  """
  attr :open, :boolean, default: false
  attr :x, :integer, default: 0
  attr :y, :integer, default: 0
  attr :close_event, :string, default: "close_context_menu"
  slot :inner_block, required: true

  def context_menu(assigns) do
    ~H"""
    <div
      :if={@open}
      id="ide-context-menu"
      class="ide-context-menu"
      style={"left: #{@x}px; top: #{@y}px;"}
      phx-click-away={@close_event}
      phx-window-keydown={@close_event}
      phx-key="escape"
      role="menu"
    >
      <ul class="flex flex-col">{render_slot(@inner_block)}</ul>
    </div>
    """
  end

  @doc "One context-menu item (wire `phx-click` via `@rest`)."
  attr :icon, :string, default: nil
  attr :danger, :boolean, default: false
  attr :rest, :global, include: ~w(phx-click phx-value-id phx-value-kind data-confirm)
  slot :inner_block, required: true

  def context_item(assigns) do
    ~H"""
    <li>
      <button
        type="button"
        role="menuitem"
        class={["ide-context-item flex w-full items-center gap-2", @danger && "ide-context-danger"]}
        {@rest}
      >
        <.icon :if={@icon} name={@icon} class="size-4 shrink-0 opacity-70" />
        <span class="flex-1 text-left">{render_slot(@inner_block)}</span>
      </button>
    </li>
    """
  end

  @doc "Horizontal viewport toolbar. Prefixed to avoid clashing with editors' private `tool_button/1`."
  attr :id, :string, default: nil
  attr :class, :string, default: nil
  slot :inner_block, required: true

  def ide_toolbar(assigns) do
    ~H"""
    <div id={@id} class={["ide-toolbar flex flex-wrap items-center gap-1.5 px-3 py-2", @class]}>
      {render_slot(@inner_block)}
    </div>
    """
  end

  @doc "A toolbar button with an icon and optional label; `active` highlights it."
  attr :id, :string, default: nil
  attr :icon, :string, required: true
  attr :label, :string, default: nil
  attr :active, :boolean, default: false
  attr :rest, :global, include: ~w(phx-click phx-value-tool phx-value-id disabled title)

  def ide_tool_button(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      class={["ide-toolbtn", @active && "ide-toolbtn-active"]}
      aria-pressed={to_string(@active)}
      {@rest}
    >
      <.icon name={@icon} class="size-4" />
      <span :if={@label} class="text-xs">{@label}</span>
    </button>
    """
  end

  @doc "A centered modal dialog. Closes on backdrop click / Escape via `on_cancel`."
  attr :id, :string, required: true
  attr :show, :boolean, default: false
  attr :on_cancel, :string, required: true, doc: "event pushed to close"
  slot :title
  slot :inner_block, required: true
  slot :footer

  def modal(assigns) do
    ~H"""
    <div
      :if={@show}
      id={@id}
      class="ide-modal-overlay fixed inset-0 z-50 flex items-center justify-center p-4"
      phx-window-keydown={@on_cancel}
      phx-key="escape"
    >
      <div class="ide-modal w-full max-w-lg rounded-box p-6 shadow-2xl" phx-click-away={@on_cancel}>
        <div :if={@title != []} class="mb-4 flex items-center justify-between">
          <h2 class="text-lg font-semibold">{render_slot(@title)}</h2>
          <button type="button" class="btn btn-ghost btn-xs btn-square" phx-click={@on_cancel}>
            <.icon name="hero-x-mark" class="size-4" />
          </button>
        </div>
        <div>{render_slot(@inner_block)}</div>
        <div :if={@footer != []} class="mt-5 flex justify-end gap-2">{render_slot(@footer)}</div>
      </div>
    </div>
    """
  end
end
