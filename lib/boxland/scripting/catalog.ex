defmodule Boxland.Scripting.Catalog do
  @moduledoc """
  Loads built-in script actions from `lib/boxland_logic/actions/*.lua`.
  Each .lua file returns a behavior descriptor with name, params, run.

  Foundation ships with an EMPTY catalog. Surface specs (Behavior Editor,
  Game Runtime) populate this directory with built-in actions.
  """

  @actions_dir Path.join([:code.priv_dir(:boxland) || "priv", "../lib/boxland_logic/actions"])

  @doc "List built-in action names available in the catalog."
  def list do
    case File.ls(@actions_dir) do
      {:ok, files} ->
        files
        |> Enum.filter(&String.ends_with?(&1, ".lua"))
        |> Enum.map(&Path.rootname/1)
        |> Enum.sort()
      _ ->
        []
    end
  end

  @doc "Load and parse a single built-in action by name."
  def load(name) when is_binary(name) do
    path = Path.join(@actions_dir, "#{name}.lua")
    if File.exists?(path) do
      source = File.read!(path)
      case Boxland.Scripting.Host.evaluate(source) do
        {:ok, [descriptor]} when is_list(descriptor) -> {:ok, descriptor}
        {:ok, _} -> {:error, :invalid_descriptor}
        {:error, reason} -> {:error, reason}
      end
    else
      {:error, :not_found}
    end
  end
end
