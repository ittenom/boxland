defmodule BoxlandWeb.DesignerRegisterLive do
  use BoxlandWeb, :live_view

  alias Boxland.Auth.Designers

  def mount(_params, _session, socket) do
    {:ok, assign(socket, form: to_form(%{}, as: :designer))}
  end

  def handle_event("register", %{"designer" => params}, socket) do
    case Designers.register_designer(params) do
      {:ok, _designer} ->
        {:noreply,
         socket
         |> put_flash(:info, "Account created. Sign in to continue.")
         |> push_navigate(to: ~p"/login")}

      {:error, changeset} ->
        {:noreply, assign(socket, form: to_form(%{changeset | action: :insert}, as: :designer))}
    end
  end

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash}>
      <section class="mx-auto max-w-md">
        <div class="mb-8">
          <span class="boxland-dot-logo text-primary" role="img" aria-label="Boxland"></span>
          <h1 class="mt-2 text-3xl font-semibold tracking-tight">Create your account</h1>
        </div>

        <.form for={@form} id="designer-register-form" phx-submit="register" class="space-y-4">
          <.input field={@form[:email]} type="email" label="Email" required />
          <.input field={@form[:display_name]} type="text" label="Display name" required />
          <.input field={@form[:password]} type="password" label="Password" required />
          <.button class="btn btn-primary w-full">Create account</.button>
        </.form>

        <p class="mt-6 text-sm text-base-content/70">
          Already have an account? <.link navigate={~p"/login"} class="link">Sign in</.link>
        </p>
      </section>
    </Layouts.app>
    """
  end
end
