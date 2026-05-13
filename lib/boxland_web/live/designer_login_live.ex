defmodule BoxlandWeb.DesignerLoginLive do
  use BoxlandWeb, :live_view

  def mount(_params, _session, socket) do
    {:ok, assign(socket, form: to_form(%{}, as: :designer))}
  end

  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash}>
      <section class="mx-auto max-w-md">
        <div class="mb-8">
          <p class="text-sm font-semibold text-primary">Boxland Designer</p>
          <h1 class="mt-2 text-3xl font-semibold tracking-tight">Sign in</h1>
        </div>

        <.form
          for={@form}
          id="designer-login-form"
          action={~p"/login"}
          method="post"
          class="space-y-4"
        >
          <.input field={@form[:email]} type="email" label="Email" required />
          <.input field={@form[:password]} type="password" label="Password" required />
          <.button class="btn btn-primary w-full">Sign in</.button>
        </.form>

        <p class="mt-6 text-sm text-base-content/70">
          New to Boxland? <.link navigate={~p"/register"} class="link">Create an account</.link>
        </p>
      </section>
    </Layouts.app>
    """
  end
end
