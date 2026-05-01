defmodule Boxland.Application do
  # See https://hexdocs.pm/elixir/Application.html
  # for more information on OTP Applications
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    if System.get_env("RUN_MIGRATIONS_ON_BOOT") == "true" do
      Boxland.Release.migrate()
    end

    children = [
      BoxlandWeb.Telemetry,
      Boxland.Repo,
      {DNSCluster, query: Application.get_env(:boxland, :dns_cluster_query) || :ignore},
      {Phoenix.PubSub, name: Boxland.PubSub},
      # Start a worker by calling: Boxland.Worker.start_link(arg)
      # {Boxland.Worker, arg},
      # Start to serve requests, typically the last entry
      BoxlandWeb.Endpoint
    ]

    # See https://hexdocs.pm/elixir/Supervisor.html
    # for other strategies and supported options
    opts = [strategy: :one_for_one, name: Boxland.Supervisor]
    Supervisor.start_link(children, opts)
  end

  # Tell Phoenix to update the endpoint configuration
  # whenever the application is updated.
  @impl true
  def config_change(changed, _new, removed) do
    BoxlandWeb.Endpoint.config_change(changed, removed)
    :ok
  end
end
