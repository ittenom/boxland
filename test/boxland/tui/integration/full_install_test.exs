defmodule Boxland.TUI.Integration.FullInstallTest do
  @moduletag :integration

  use ExUnit.Case, async: false

  alias Boxland.TUI.Install

  setup do
    tmp = Path.join(System.tmp_dir!(), "boxland-e2e-#{System.unique_integer([:positive])}")
    File.mkdir_p!(tmp)
    File.chmod!(tmp, 0o700)

    on_exit(fn ->
      # Tear down the compose stack started by Install. Default project
      # name derives from the compose file's parent dir, so omitting -p
      # matches what `docker compose up -d` used.
      compose_file = Path.join(tmp, "services/docker-compose.yml")

      if File.exists?(compose_file) do
        System.cmd("docker", ["compose", "-f", compose_file, "down", "-v"])
      end

      File.rm_rf!(tmp)
    end)

    {:ok, tmp: tmp}
  end

  test "full install: 9 stages run, marker written, server starts", %{tmp: tmp} do
    # Build a complete deps map (Install.default_deps is private; we
    # construct an equivalent inline, overriding data_dir for isolation).
    real_run = fn ->
      Install.run(%{
        os: &Boxland.TUI.Install.System.detect_os/0,
        which: &Boxland.TUI.Install.System.which/1,
        run_cmd: &System.cmd/3,
        data_dir: fn -> tmp end,
        file_exists: &File.exists?/1,
        file_write: &File.write/2,
        mkdir_p: &File.mkdir_p/1,
        chmod: &File.chmod/2,
        sleep: &Process.sleep/1,
        max_health_polls: 30,
        release_migrate: &Boxland.Release.migrate/0,
        now: &DateTime.utc_now/0,
        version: fn -> to_string(Application.spec(:boxland, :vsn)) end
      })
    end

    case real_run.() do
      {:ok, _report} -> :ok
      {:error, err} -> flunk("Install failed: #{inspect(err)}")
    end

    assert File.exists?(Path.join(tmp, "installed"))

    # Start server, hit /healthz
    Boxland.Server.Supervisor.start_children()
    Process.sleep(500)

    case System.cmd(
           "curl",
           ["-s", "-o", "/dev/null", "-w", "%{http_code}", "http://localhost:4000/healthz"],
           stderr_to_stdout: true
         ) do
      {"200", 0} -> :ok
      {other, _} -> flunk("Expected 200 from /healthz, got: #{other}")
    end

    Boxland.Server.Supervisor.stop_children()
  end
end
