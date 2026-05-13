defmodule BoxlandWeb.DashboardLive do
  use BoxlandWeb, :live_view

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
    <Layouts.app flash={@flash} current_scope={%{designer: @current_designer}}>
      <section class="space-y-8">
        <div class="flex items-center justify-between gap-4">
          <div>
            <p class="text-sm font-semibold text-primary">Designer workspace</p>
            <h1 class="text-3xl font-semibold tracking-tight">Build a Boxland level</h1>
          </div>
          <.link href={~p"/logout"} method="delete" class="btn btn-ghost">Sign out</.link>
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
    </Layouts.app>
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
