defmodule Boxland.CLI.Run do
  @moduledoc """
  `boxland run` subcommand — start the server in foreground, blocking
  until SIGINT/SIGTERM.
  """

  alias Boxland.Server.Supervisor, as: Sup

  def start do
    Sup.start_children()
  end

  def main(_argv) do
    start()
    IO.puts("Boxland server running. Press Ctrl-C to stop.")
    Process.sleep(:infinity)
  end
end
