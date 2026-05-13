defmodule BoxlandWeb.DesignerRegistrationController do
  use BoxlandWeb, :controller

  alias Boxland.Auth.Designers
  alias BoxlandWeb.DesignerAuth

  def create(conn, %{"designer" => params}) do
    case Designers.register_designer(params) do
      {:ok, designer} ->
        conn
        |> DesignerAuth.log_in_designer(designer, remote_ip(conn))
        |> put_flash(:info, "Account created.")
        |> redirect(to: ~p"/app")

      {:error, changeset} ->
        conn
        |> put_flash(:error, register_error(changeset))
        |> redirect(to: ~p"/register")
    end
  end

  def create(conn, _params) do
    conn
    |> put_flash(:error, "Enter an email, display name, and password.")
    |> redirect(to: ~p"/register")
  end

  defp register_error(changeset) do
    changeset.errors
    |> Enum.map(fn {field, {message, opts}} ->
      label = field |> Atom.to_string() |> String.replace("_", " ") |> String.capitalize()
      "#{label} #{BoxlandWeb.CoreComponents.translate_error({message, opts})}"
    end)
    |> case do
      [] -> "Account could not be created."
      messages -> Enum.join(messages, " ")
    end
  end

  defp remote_ip(conn), do: conn.remote_ip |> :inet.ntoa() |> to_string()
end
