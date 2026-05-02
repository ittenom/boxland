defmodule Boxland.TUI.Install do
  @moduledoc """
  9-stage idempotent install workflow.

  Each stage takes a deps map (default: `default_deps/0`) so tests can
  inject mock implementations of System.cmd, File ops, and OS detection.

  Stages:
    1. pre-flight scan          (read-only; produces a report)
    2. OS packages              (install missing libvips)
    3. Docker check             (verify daemon)
    4. data directory           (mkdir -p ~/.boxland/services/...)
    5. secrets                  (generate SECRET_KEY_BASE)
    6. config file              (~/.boxland/config.exs)
    7. docker-compose template  (~/.boxland/services/docker-compose.yml)
    8. bring up services        (docker compose up -d + health poll)
    9. migrations               (Boxland.Release.migrate/0)
  """

  # ---------- Stage 1: Pre-flight scan ----------

  def stage_1_preflight(deps \\ default_deps()) do
    {os, arch} = deps.os.()
    pm = detect_pm(os, deps.which)

    report = %{
      os: {os, arch},
      package_manager: pm,
      installed: %{
        docker: deps.which.("docker") != :error,
        libvips: deps.which.("vips") != :error
      }
    }

    {:ok, report}
  end

  defp detect_pm(:darwin, which) do
    if which.("brew") != :error, do: :brew, else: nil
  end

  defp detect_pm(:linux, which) do
    cond do
      which.("apt-get") != :error -> :apt
      which.("dnf") != :error -> :dnf
      which.("yum") != :error -> :yum
      true -> nil
    end
  end

  defp detect_pm(_, _), do: nil

  # ---------- Stage 2: OS packages ----------

  def stage_2_os_packages(report, deps \\ default_deps())

  def stage_2_os_packages(%{installed: %{libvips: true}}, _deps), do: :ok

  def stage_2_os_packages(%{installed: %{libvips: false}, package_manager: :brew}, deps) do
    case deps.run_cmd.("brew", ["install", "vips"], stderr_to_stdout: true) do
      {_, 0} -> :ok
      {output, code} ->
        {:error, %{
          stage: :os_packages,
          reason: "brew install vips failed (exit #{code}): #{output}",
          suggestion: nil
        }}
    end
  end

  def stage_2_os_packages(%{installed: %{libvips: false}, package_manager: pm}, _deps)
      when pm in [:apt, :dnf, :yum] do
    cmd = case pm do
      :apt -> "sudo apt install libvips42"
      :dnf -> "sudo dnf install vips"
      :yum -> "sudo yum install vips"
    end
    {:error, %{
      stage: :os_packages,
      reason: "libvips missing; install requires sudo and isn't escalated automatically",
      suggestion: "Run: #{cmd}, then re-run Boxland Install."
    }}
  end

  def stage_2_os_packages(%{installed: %{libvips: false}, package_manager: nil}, _deps) do
    {:error, %{
      stage: :os_packages,
      reason: "libvips missing and no supported package manager detected",
      suggestion: "Install libvips manually for your platform, then re-run Boxland Install."
    }}
  end

  # ---------- Stage 3: Docker check ----------

  def stage_3_docker_check(deps \\ default_deps()) do
    case deps.run_cmd.("docker", ["info"], stderr_to_stdout: true) do
      {_, 0} ->
        :ok

      {output, _code} ->
        suggestion = "Install Docker Desktop from https://docker.com/products/docker-desktop and ensure it's running, then retry."
        {:error, %{
          stage: :docker_check,
          reason: String.trim(output) |> String.slice(0, 300),
          suggestion: suggestion
        }}
    end
  end

  # ---------- Stage 4: Data directory ----------

  def stage_4_data_directory(deps \\ default_deps()) do
    base = deps.data_dir.()
    services = Path.join(base, "services")

    with :ok <- deps.mkdir_p.(Path.join(services, "pg_data")),
         :ok <- deps.mkdir_p.(Path.join(services, "minio_data")),
         :ok <- deps.chmod.(base, 0o700) do
      :ok
    else
      {:error, reason} ->
        {:error, %{stage: :data_directory, reason: "Failed: #{inspect(reason)}", suggestion: nil}}
    end
  end

  # ---------- Stage 5: Secrets ----------

  def stage_5_secrets(deps \\ default_deps()) do
    path = Path.join(deps.data_dir.(), "secrets.exs")

    if deps.file_exists.(path) do
      :ok
    else
      secret = :crypto.strong_rand_bytes(64) |> Base.url_encode64(padding: false)
      content = """
      import Config

      # Generated on Install. Regenerating requires DELETING this file
      # (subsequent Install rewrites it) — note: any session tokens
      # signed with the old secret will be invalidated.

      config :boxland, BoxlandWeb.Endpoint,
        secret_key_base: "#{secret}"
      """

      with :ok <- deps.file_write.(path, content),
           :ok <- deps.chmod.(path, 0o600) do
        :ok
      else
        {:error, reason} ->
          {:error, %{stage: :secrets, reason: "Failed: #{inspect(reason)}", suggestion: nil}}
      end
    end
  end

  # ---------- Stage 6: Config file ----------

  def stage_6_config(deps \\ default_deps()) do
    path = Path.join(deps.data_dir.(), "config.exs")

    if deps.file_exists.(path) do
      :ok
    else
      template_path = Application.app_dir(:boxland, "priv/templates/user_config.exs.eex")
      content = EEx.eval_file(template_path)

      case deps.file_write.(path, content) do
        :ok -> :ok
        {:error, reason} ->
          {:error, %{stage: :config, reason: "Failed: #{inspect(reason)}", suggestion: nil}}
      end
    end
  end

  # ---------- Stage 7: docker-compose template ----------

  def stage_7_compose(deps \\ default_deps()) do
    base = deps.data_dir.()
    path = Path.join(base, "services/docker-compose.yml")

    template_path = Application.app_dir(:boxland, "priv/templates/docker_compose.yml.eex")
    content = EEx.eval_file(template_path, assigns: [data_dir: base])

    case deps.file_write.(path, content) do
      :ok -> :ok
      {:error, reason} ->
        {:error, %{stage: :compose, reason: "Failed: #{inspect(reason)}", suggestion: nil}}
    end
  end

  # ---------- Stage 8: Bring up services ----------

  def stage_8_services(deps \\ default_deps()) do
    base = deps.data_dir.()
    compose_file = Path.join(base, "services/docker-compose.yml")
    max_polls = Map.get(deps, :max_health_polls, 30)

    with {_, 0} <- deps.run_cmd.("docker", ["compose", "-f", compose_file, "up", "-d"], stderr_to_stdout: true),
         :ok <- poll_health(compose_file, max_polls, deps) do
      :ok
    else
      {output, code} when is_binary(output) ->
        {:error, %{
          stage: :services,
          reason: "docker compose up failed (exit #{code}): #{String.slice(output, 0, 300)}",
          suggestion: "Verify Docker daemon is running and ports 5432/6379/9000/9001 are free, then retry."
        }}

      {:error, reason} ->
        {:error, %{stage: :services, reason: reason, suggestion: nil}}
    end
  end

  defp poll_health(compose_file, polls_remaining, deps) when polls_remaining > 0 do
    case deps.run_cmd.("docker", ["compose", "-f", compose_file, "ps", "--format", "json"], stderr_to_stdout: true) do
      {output, 0} ->
        if all_critical_healthy?(output) do
          :ok
        else
          deps.sleep.(1000)
          poll_health(compose_file, polls_remaining - 1, deps)
        end

      {_output, _} ->
        deps.sleep.(1000)
        poll_health(compose_file, polls_remaining - 1, deps)
    end
  end

  defp poll_health(_compose_file, 0, _deps) do
    {:error, "Timeout waiting for postgres + redis to become healthy"}
  end

  # We require postgres + redis healthy. minio has no built-in healthcheck,
  # so we don't gate on it.
  defp all_critical_healthy?(json_lines) do
    statuses =
      json_lines
      |> String.split("\n", trim: true)
      |> Enum.flat_map(fn line ->
        case Jason.decode(line) do
          {:ok, %{"Service" => svc, "Health" => h}} -> [{svc, h}]
          _ -> []
        end
      end)
      |> Map.new()

    Map.get(statuses, "postgres") == "healthy" and Map.get(statuses, "redis") == "healthy"
  end

  # ---------- Defaults ----------

  defp default_deps do
    %{
      os: &Boxland.TUI.Install.System.detect_os/0,
      which: &Boxland.TUI.Install.System.which/1,
      run_cmd: &System.cmd/3,
      data_dir: fn -> Path.expand("~/.boxland") end,
      now: &DateTime.utc_now/0,
      version: fn -> to_string(Application.spec(:boxland, :vsn)) end,
      file_write: &File.write/2,
      file_exists: &File.exists?/1,
      mkdir_p: &File.mkdir_p/1,
      chmod: &File.chmod/2,
      release_migrate: &Boxland.Release.migrate/0,
      sleep: &:timer.sleep/1
    }
  end
end

defmodule Boxland.TUI.Install.System do
  @moduledoc false

  def detect_os do
    case :os.type() do
      {:unix, :darwin} -> {:darwin, normalize_arch()}
      {:unix, :linux} -> {:linux, normalize_arch()}
      other -> other
    end
  end

  defp normalize_arch do
    case to_string(:erlang.system_info(:system_architecture)) do
      "aarch64-" <> _ -> :aarch64
      "arm64-" <> _ -> :aarch64
      "x86_64-" <> _ -> :x86_64
      _ -> :unknown
    end
  end

  def which(cmd) when is_binary(cmd) do
    case System.find_executable(cmd) do
      nil -> :error
      path -> {:ok, path}
    end
  end
end
