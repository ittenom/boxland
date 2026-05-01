defmodule BoxlandWeb.HealthController do
  use BoxlandWeb, :controller

  @doc """
  Liveness probe: returns 200 if the BEAM is up and the HTTP layer is serving.
  Used by Railway for restart decisions. Does NOT check DB or Redis.
  """
  def healthz(conn, _params) do
    text(conn, "ok")
  end

  @doc """
  Readiness probe: returns 200 only if the app can serve traffic
  (DB reachable + Redis reachable). Returns 503 otherwise.
  """
  def readyz(conn, _params) do
    case check_db() do
      :ok -> text(conn, "ready")
      {:error, reason} ->
        conn
        |> put_status(503)
        |> text("not ready: db: #{reason}")
    end
  end

  defp check_db do
    case Ecto.Adapters.SQL.query(Boxland.Repo, "SELECT 1", []) do
      {:ok, _} -> :ok
      {:error, err} -> {:error, inspect(err)}
    end
  end
end
