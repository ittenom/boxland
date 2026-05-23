defmodule Boxland.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    if System.get_env("RUN_MIGRATIONS_ON_BOOT") == "true" do
      Boxland.Release.migrate()
    end

    children = [
      Boxland.Repo,
      {Phoenix.PubSub, name: Boxland.PubSub},
      {Finch, name: Boxland.Finch},
      {DNSCluster, query: Application.get_env(:boxland, :dns_cluster_query) || :ignore},
      BoxlandWeb.Telemetry,
      BoxlandWeb.Endpoint
    ]

    opts = [strategy: :one_for_one, name: Boxland.Supervisor]
    Supervisor.start_link(children, opts)
  end

  @impl true
  def config_change(changed, _new, removed) do
    BoxlandWeb.Endpoint.config_change(changed, removed)
    :ok
  end
end
