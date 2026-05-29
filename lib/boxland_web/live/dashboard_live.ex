defmodule BoxlandWeb.DashboardLive do
  use BoxlandWeb, :live_view

  import BoxlandWeb.Components.Ide

  alias Boxland.{Levels, Library, Maps}

  def mount(_params, _session, socket) do
    designer = socket.assigns.current_designer

    {:ok,
     socket
     |> assign(:assets, Library.list_assets(designer.id))
     |> assign(:maps, Maps.list_maps(designer.id))
     |> assign(:levels, Levels.list_levels(designer.id))}
  end

  def render(assigns) do
    ~H"""
    <div id="dashboard-root">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:workspace} />
          <div class="flex-1"></div>
          <.link href={~p"/logout"} method="delete" class="ide-rail-item" title="Sign out">
            <.icon name="hero-arrow-right-start-on-rectangle" class="size-5" />
          </.link>
        </:activity>

        <:viewport>
          <div class="min-h-0 flex-1 overflow-auto p-8">
            <section class="mx-auto max-w-5xl space-y-8">
              <div>
                <p class="text-sm font-semibold text-primary">Designer workspace</p>
                <h1 class="text-3xl font-semibold tracking-tight">Build a Boxland level</h1>
              </div>

              <div class="grid gap-4 md:grid-cols-4">
                <.workflow_card
                  title="1. Upload tileset"
                  count={length(@assets)}
                  href={~p"/app/assets"}
                  action="Open Assets"
                />
                <.workflow_card
                  title="2. Design map"
                  count={length(@maps)}
                  href={~p"/app/maps"}
                  action="Open Mapmaker"
                />
                <.workflow_card
                  title="3. Edit level"
                  count={length(@levels)}
                  href={~p"/app/levels"}
                  action="Open Levels"
                />
                <.workflow_card
                  title="4. Sandbox & publish"
                  count={Enum.count(@levels)}
                  href={~p"/app/levels"}
                  action="Test"
                />
              </div>
            </section>
          </div>
        </:viewport>

        <:status>
          <span class="font-mono">
            {length(@assets)} assets · {length(@maps)} maps · {length(@levels)} levels
          </span>
        </:status>
      </.ide_shell>
    </div>
    """
  end

  attr :title, :string, required: true
  attr :count, :integer, required: true
  attr :href, :string, required: true
  attr :action, :string, required: true

  defp workflow_card(assigns) do
    ~H"""
    <.link navigate={@href} class="card bg-base-200 transition hover:bg-base-300">
      <div class="card-body">
        <h2 class="card-title text-base">{@title}</h2>
        <p class="text-3xl font-semibold">{@count}</p>
        <span class="btn btn-primary btn-sm mt-2 w-fit">{@action}</span>
      </div>
    </.link>
    """
  end
end
