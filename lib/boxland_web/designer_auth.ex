defmodule BoxlandWeb.DesignerAuth do
  @moduledoc "Designer session helpers for controllers and LiveViews."

  import Phoenix.Controller
  import Plug.Conn

  alias Boxland.Auth.Designers

  @session_key "designer_token"

  def log_in_designer(conn, designer, ip) do
    {:ok, token} = Designers.create_session(designer.id, ip)

    conn
    |> renew_session()
    |> put_session(@session_key, token)
  end

  def log_out_designer(conn) do
    if token = get_session(conn, @session_key), do: Designers.revoke_session(token)

    conn
    |> renew_session()
    |> redirect(to: "/")
  end

  def fetch_current_designer(conn, _opts) do
    designer =
      with token when is_binary(token) <- get_session(conn, @session_key),
           {:ok, designer} <- Designers.fetch_session(token) do
        designer
      else
        _ -> nil
      end

    assign(conn, :current_designer, designer)
  end

  def require_designer(conn, _opts) do
    if conn.assigns[:current_designer] do
      conn
    else
      conn
      |> put_flash(:error, "Sign in to continue.")
      |> redirect(to: "/login")
      |> halt()
    end
  end

  def on_mount(:require_designer, _params, session, socket) do
    case fetch_designer(session) do
      nil ->
        {:halt, Phoenix.LiveView.redirect(socket, to: "/login")}

      designer ->
        {:cont, Phoenix.Component.assign(socket, :current_designer, designer)}
    end
  end

  def on_mount(:mount_current_designer, _params, session, socket) do
    {:cont, Phoenix.Component.assign(socket, :current_designer, fetch_designer(session))}
  end

  defp fetch_designer(session) do
    with token when is_binary(token) <- Map.get(session, @session_key),
         {:ok, designer} <- Designers.fetch_session(token) do
      designer
    else
      _ -> nil
    end
  end

  defp renew_session(conn) do
    conn
    |> configure_session(renew: true)
    |> clear_session()
  end
end
