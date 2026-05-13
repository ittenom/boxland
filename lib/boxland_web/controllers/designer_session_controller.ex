defmodule BoxlandWeb.DesignerSessionController do
  use BoxlandWeb, :controller

  alias Boxland.Auth.Designers
  alias BoxlandWeb.DesignerAuth

  def create(conn, %{"designer" => %{"email" => email, "password" => password}}) do
    case Designers.authenticate(email, password) do
      {:ok, designer} ->
        conn
        |> DesignerAuth.log_in_designer(designer, remote_ip(conn))
        |> redirect(to: ~p"/app")

      :error ->
        conn
        |> put_flash(:error, "Invalid email or password.")
        |> redirect(to: ~p"/login")
    end
  end

  def delete(conn, _params), do: DesignerAuth.log_out_designer(conn)

  defp remote_ip(conn), do: conn.remote_ip |> :inet.ntoa() |> to_string()
end
