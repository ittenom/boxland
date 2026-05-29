defmodule BoxlandWeb.LevelIndexLive do
  use BoxlandWeb, :live_view

  import BoxlandWeb.Components.Ide

  alias Boxland.{Levels, Maps}

  def mount(_params, _session, socket) do
    designer = socket.assigns.current_designer
    maps = Maps.list_maps(designer.id)

    {:ok,
     socket
     |> assign(:levels, Levels.list_levels(designer.id))
     |> assign(:maps, maps)
     |> assign(
       :form,
       to_form(%{"map_id" => maps |> List.first() |> then(&(&1 && &1.id))}, as: :level)
     )}
  end

  def handle_event("create", %{"level" => params}, socket) do
    case Levels.create_level(socket.assigns.current_designer.id, params) do
      {:ok, level} ->
        {:noreply, push_navigate(socket, to: ~p"/app/levels/#{level.id}")}

      {:error, changeset} ->
        {:noreply, assign(socket, :form, to_form(changeset, as: :level))}
    end
  end

  def render(assigns) do
    ~H"""
    <div id="level-index-root">
      <.ide_shell flash={@flash}>
        <:activity>
          <.ide_rail_nav active={:levels} />
        </:activity>

        <:viewport>
          <div class="min-h-0 flex-1 overflow-auto p-8">
            <section class="mx-auto max-w-5xl space-y-6">
              <div>
                <p class="text-sm font-semibold text-primary">Level Editor</p>
                <h1 class="text-3xl font-semibold tracking-tight">Levels</h1>
              </div>

              <.form for={@form} id="level-create-form" phx-submit="create" class="card bg-base-200">
                <div class="card-body grid gap-4 md:grid-cols-5">
                  <.input field={@form[:name]} type="text" label="Name" required />
                  <.input field={@form[:slug]} type="text" label="Slug" required />
                  <.input
                    field={@form[:map_id]}
                    type="select"
                    label="Map"
                    options={Enum.map(@maps, &{&1.name, &1.id})}
                    required
                  />
                  <.input
                    field={@form[:instancing]}
                    type="select"
                    label="Instancing"
                    options={[
                      {"Shared", "shared"},
                      {"Per user", "per_user"},
                      {"Per party", "per_party"}
                    ]}
                  />
                  <div class="flex items-end">
                    <.button class="btn btn-primary w-full">Create level</.button>
                  </div>
                </div>
              </.form>

              <div class="grid gap-4 md:grid-cols-3">
                <.link
                  :for={level <- @levels}
                  navigate={~p"/app/levels/#{level.id}"}
                  class="card bg-base-200 hover:bg-base-300"
                >
                  <div class="card-body">
                    <h2 class="card-title">{level.name}</h2>
                    <p class="text-sm text-base-content/60">{level.map.name}</p>
                  </div>
                </.link>
              </div>
            </section>
          </div>
        </:viewport>

        <:status>
          <span class="font-mono">
            {length(@levels)} level{if length(@levels) == 1, do: "", else: "s"}
          </span>
        </:status>
      </.ide_shell>
    </div>
    """
  end
end
