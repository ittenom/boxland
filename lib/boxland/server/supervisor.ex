defmodule Boxland.Server.Supervisor do
  @moduledoc """
  Sub-supervisor for the Phoenix-side runtime children. Boots empty;
  the TUI calls `start_children/0` when "Run Server" toggles ON and
  `stop_children/0` on toggle OFF or quit.

  Strategy: rest_for_one — Endpoint depends on nothing; DNSCluster and
  Telemetry layer on. If a later child crashes, only it (and any after)
  restart; Endpoint stays up.
  """
  use Supervisor

  alias BoxlandWeb.Endpoint, as: WebEndpoint
  alias BoxlandWeb.Telemetry, as: WebTelemetry

  def start_link(_opts) do
    Supervisor.start_link(__MODULE__, :ok, name: __MODULE__)
  end

  @impl true
  def init(:ok) do
    Supervisor.init([], strategy: :rest_for_one)
  end

  @doc """
  Start the Phoenix runtime children. Idempotent: returns :ok whether
  children were already running or are newly started.
  """
  def start_children do
    children = phoenix_children()

    Enum.each(children, fn child_spec ->
      case Supervisor.start_child(__MODULE__, child_spec) do
        {:ok, _pid} -> :ok
        {:error, {:already_started, _pid}} -> :ok
        {:error, :already_present} -> :ok
        other -> raise "Failed to start child #{inspect(child_spec.id)}: #{inspect(other)}"
      end
    end)

    :ok
  end

  @doc """
  Stop the Phoenix runtime children in reverse order. Idempotent.
  """
  def stop_children do
    Supervisor.which_children(__MODULE__)
    |> Enum.reverse()
    |> Enum.each(fn {id, _pid, _type, _modules} ->
      _ = Supervisor.terminate_child(__MODULE__, id)
      _ = Supervisor.delete_child(__MODULE__, id)
    end)

    :ok
  end

  @doc "Returns :running when at least one child is active, else :stopped."
  def status do
    case Supervisor.count_children(__MODULE__) do
      %{active: count} when count > 0 -> :running
      _ -> :stopped
    end
  end

  defp phoenix_children do
    [
      Supervisor.child_spec(
        {DNSCluster, query: Application.get_env(:boxland, :dns_cluster_query) || :ignore},
        id: :dns_cluster
      ),
      Supervisor.child_spec(WebTelemetry, id: :telemetry),
      Supervisor.child_spec(WebEndpoint, id: :endpoint)
    ]
  end
end
