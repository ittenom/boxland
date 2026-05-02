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
      release_migrate: &Boxland.Release.migrate/0
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
