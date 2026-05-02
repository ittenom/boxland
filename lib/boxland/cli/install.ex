defmodule Boxland.CLI.Install do
  @moduledoc """
  `boxland install` subcommand — non-interactive Install workflow.
  """

  alias Boxland.TUI.Install

  def main(_argv, deps \\ %{install_run: &Install.run/0}) do
    IO.puts("Boxland Install — running 9 stages…")

    case deps.install_run.([]) do
      {:ok, _report} ->
        IO.puts("Install complete.")
        0

      {:error, %{stage: stage, reason: reason} = err} ->
        IO.puts("FAILED at stage: #{stage}")
        IO.puts("  #{reason}")
        if Map.get(err, :suggestion), do: IO.puts("\n#{err.suggestion}")
        1
    end
  end
end
