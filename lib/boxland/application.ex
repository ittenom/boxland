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
      Boxland.Repo,
      {Phoenix.PubSub, name: Boxland.PubSub},
      {Finch, name: Boxland.Finch},
      %{id: Boxland.TUI.LogBackend, start: {Boxland.TUI.LogBackend, :start_link, [[]]}, restart: :transient},  # ADD this
      Boxland.Server.Supervisor
    ]

    # See https://hexdocs.pm/elixir/Supervisor.html
    # for other strategies and supported options
    opts = [strategy: :one_for_one, name: Boxland.Supervisor]

    case Supervisor.start_link(children, opts) do
      {:ok, sup} ->
        dispatch_argv()
        {:ok, sup}

      other -> other
    end
  end

  defp dispatch_argv do
    if Code.ensure_loaded?(Mix) and Mix.env() == :test do
      :ok   # Don't dispatch during tests
    else
      do_dispatch_argv()
    end
  end

  defp do_dispatch_argv do
    case System.argv() do
      [] -> Boxland.TUI.Server.start_link()       # default: open TUI

      ["start" | _] ->
        # Release boot via `bin/boxland start` — launch TUI as the foreground process.
        Boxland.TUI.Server.start_link()

      ["install" | argv] -> System.halt(Boxland.CLI.Install.main(argv))
      ["run" | argv] -> Boxland.CLI.Run.main(argv)
      ["--version"] ->
        IO.puts("boxland #{Application.spec(:boxland, :vsn)}")
        System.halt(0)
      other ->
        IO.puts(:stderr, "Unknown command: #{Enum.join(other, " ")}")
        IO.puts(:stderr, "Usage: boxland [install | run | --version]")
        System.halt(1)
    end
  end

  # Tell Phoenix to update the endpoint configuration
  # whenever the application is updated.
  @impl true
  def config_change(changed, _new, removed) do
    BoxlandWeb.Endpoint.config_change(changed, removed)
    :ok
  end
end
