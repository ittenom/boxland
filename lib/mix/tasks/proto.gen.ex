defmodule Mix.Tasks.Proto.Gen do
  @moduledoc """
  Generate Elixir + TypeScript modules from .proto schemas.

  Usage:
      mix proto.gen           # generate
      mix proto.gen --check   # verify generated files are up-to-date (CI)
  """
  use Mix.Task

  @schemas_dir "schemas"
  @elixir_out "lib/boxland_web/proto"
  @ts_out "assets/js/proto"

  def run(args) do
    check = "--check" in args

    File.mkdir_p!(@elixir_out)
    File.mkdir_p!(@ts_out)

    proto_files =
      Path.wildcard("#{@schemas_dir}/*.proto")
      |> Enum.sort()

    if Enum.empty?(proto_files) do
      Mix.shell().error("No .proto files found in #{@schemas_dir}/")
      exit({:shutdown, 1})
    end

    if check do
      run_check(proto_files)
    else
      run_generate(proto_files)
    end
  end

  defp run_generate(proto_files) do
    # Elixir generation via protoc-gen-elixir
    # Use --plugin flag to explicitly locate protoc-gen-elixir since ~/.mix/escripts
    # is not on PATH by default in the System.cmd environment.
    elixir_plugin = Path.expand("~/.mix/escripts/protoc-gen-elixir")

    elixir_args = [
      "--proto_path=#{@schemas_dir}",
      "--plugin=protoc-gen-elixir=#{elixir_plugin}",
      "--elixir_out=plugins=grpc:#{@elixir_out}"
      | proto_files
    ]

    case System.cmd("protoc", elixir_args, stderr_to_stdout: true) do
      {_, 0} ->
        :ok

      {out, code} ->
        Mix.shell().error("protoc (elixir) failed (exit #{code}):\n#{out}")
        exit({:shutdown, code})
    end

    # TypeScript generation via ts-proto
    ts_plugin = Path.expand("assets/node_modules/.bin/protoc-gen-ts_proto")

    ts_args = [
      "--proto_path=#{@schemas_dir}",
      "--plugin=protoc-gen-ts_proto=#{ts_plugin}",
      "--ts_proto_out=#{@ts_out}",
      "--ts_proto_opt=esModuleInterop=true,outputServices=false,useOptionals=messages"
      | proto_files
    ]

    case System.cmd("protoc", ts_args, stderr_to_stdout: true) do
      {_, 0} ->
        Mix.shell().info("Generated proto modules to #{@elixir_out} and #{@ts_out}")

      {out, code} ->
        Mix.shell().error("protoc (ts) failed (exit #{code}):\n#{out}")
        exit({:shutdown, code})
    end
  end

  defp run_check(proto_files) do
    snapshot_elixir = snapshot_dir(@elixir_out)
    snapshot_ts = snapshot_dir(@ts_out)

    run_generate(proto_files)

    fresh_elixir = snapshot_dir(@elixir_out)
    fresh_ts = snapshot_dir(@ts_out)

    if snapshot_elixir == fresh_elixir and snapshot_ts == fresh_ts do
      Mix.shell().info("Generated proto modules are up-to-date.")
    else
      Mix.shell().error("Generated proto modules are STALE. Run `mix proto.gen` and commit.")
      exit({:shutdown, 1})
    end
  end

  defp snapshot_dir(dir) do
    case File.ls(dir) do
      {:ok, files} ->
        files
        |> Enum.sort()
        |> Enum.map(fn f -> {f, File.read!(Path.join(dir, f))} end)

      _ ->
        []
    end
  end
end
