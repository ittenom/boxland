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
      %{
        id: Boxland.TUI.LogBackend,
        start: {Boxland.TUI.LogBackend, :start_link, [[]]},
        restart: :transient
      },
      Boxland.Server.Supervisor
    ]

    opts = [strategy: :one_for_one, name: Boxland.Supervisor]

    case Supervisor.start_link(children, opts) do
      {:ok, sup} ->
        attach_logger_handler()
        dispatch_argv()
        {:ok, sup}

      other ->
        other
    end
  end

  defp attach_logger_handler do
    if not (Code.ensure_loaded?(Mix) and Mix.env() == :test) do
      _ = :logger.add_handler(:boxland_tui, Boxland.TUI.LoggerHandler, %{})
    end

    :ok
  end

  defp dispatch_argv do
    if Code.ensure_loaded?(Mix) and Mix.env() == :test do
      :ok
    else
      do_dispatch_argv()
    end
  end

  defp do_dispatch_argv do
    case System.argv() do
      ["install" | argv] ->
        System.halt(Boxland.CLI.Install.main(argv))

      ["run" | argv] ->
        Boxland.CLI.Run.main(argv)

      ["--version"] ->
        IO.puts("boxland #{Application.spec(:boxland, :vsn)}")
        System.halt(0)

      argv when argv == [] or hd(argv) == "start" ->
        spawn_tui()

      other ->
        IO.puts(:stderr, "Unknown command: #{Enum.join(other, " ")}")
        IO.puts(:stderr, "Usage: boxland [install | run | --version]")
        System.halt(1)
    end
  end

  defp spawn_tui do
    spawn(fn ->
      _ = TermUI.Runtime.run(root: Boxland.TUI.App)
      System.halt(0)
    end)

    :ok
  end

  @impl true
  def config_change(changed, _new, removed) do
    BoxlandWeb.Endpoint.config_change(changed, removed)
    :ok
  end
end
