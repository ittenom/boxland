defmodule BoxlandWeb.MapIndexLive do
  use BoxlandWeb, :live_view

  import BoxlandWeb.Components.Ide

  alias Boxland.Maps

  def mount(_params, _session, socket) do
    designer = socket.assigns.current_designer

    {:ok,
     socket
     |> assign(:maps, Maps.list_maps(designer.id))
     |> assign(:form, to_form(%{"width" => "20", "height" => "15"}, as: :map))}
  end

  def handle_event("create", %{"map" => params}, socket) do
    case Maps.create_map(socket.assigns.current_designer.id, params) do
      {:ok, map} ->
        {:noreply, push_navigate(socket, to: ~p"/app/maps/#{map.id}")}

      {:error, changeset} ->
        {:noreply, assign(socket, :form, to_form(changeset, as: :map))}
    end
  end

  def render(assigns) do
    ~H"""
    <div id="map-index-root">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:maps} />
        </:activity>

        <:viewport>
          <div class="min-h-0 flex-1 overflow-auto p-8">
            <section class="mx-auto max-w-5xl space-y-6">
              <div>
                <p class="text-sm font-semibold text-primary">Mapmaker</p>
                <h1 class="text-3xl font-semibold tracking-tight">Maps</h1>
              </div>

              <.form for={@form} id="map-create-form" phx-submit="create" class="card bg-base-200">
                <div class="card-body grid gap-4 md:grid-cols-5">
                  <.input field={@form[:name]} type="text" label="Name" required />
                  <.input field={@form[:slug]} type="text" label="Slug" required />
                  <.input field={@form[:width]} type="number" label="Width cells" required min="1" />
                  <.input field={@form[:height]} type="number" label="Height cells" required min="1" />
                  <div class="flex items-end">
                    <.button class="btn btn-primary w-full">Create map</.button>
                  </div>
                </div>
              </.form>

              <div class="grid gap-4 md:grid-cols-3">
                <.link
                  :for={map <- @maps}
                  navigate={~p"/app/maps/#{map.id}"}
                  class="card bg-base-200 hover:bg-base-300"
                >
                  <div class="card-body">
                    <h2 class="card-title">{map.name}</h2>
                    <p class="text-sm text-base-content/60">{map.width} x {map.height} cells</p>
                  </div>
                </.link>
              </div>
            </section>
          </div>
        </:viewport>

        <:status>
          <span class="font-mono">{length(@maps)} map{if length(@maps) == 1, do: "", else: "s"}</span>
        </:status>
      </.ide_shell>
    </div>
    """
  end
end
