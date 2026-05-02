defmodule Boxland.TUI.InstallTest do
  use ExUnit.Case, async: true
  alias Boxland.TUI.Install

  describe "stage_1_preflight/1" do
    test "detects Mac arm64 + Homebrew" do
      deps = %{
        os: fn -> {:darwin, :aarch64} end,
        which: fn
          "brew" -> {:ok, "/opt/homebrew/bin/brew"}
          "docker" -> {:ok, "/usr/local/bin/docker"}
          "vips" -> {:ok, "/opt/homebrew/bin/vips"}
          _ -> :error
        end
      }
      assert {:ok, report} = Install.stage_1_preflight(deps)
      assert report.os == {:darwin, :aarch64}
      assert report.package_manager == :brew
      assert report.installed.docker == true
      assert report.installed.libvips == true
    end

    test "detects Linux amd64 + apt" do
      deps = %{
        os: fn -> {:linux, :x86_64} end,
        which: fn
          "apt-get" -> {:ok, "/usr/bin/apt-get"}
          "docker" -> {:ok, "/usr/bin/docker"}
          _ -> :error
        end
      }
      assert {:ok, report} = Install.stage_1_preflight(deps)
      assert report.os == {:linux, :x86_64}
      assert report.package_manager == :apt
      assert report.installed.libvips == false
    end

    test "no package manager available" do
      deps = %{
        os: fn -> {:linux, :x86_64} end,
        which: fn _ -> :error end
      }
      assert {:ok, report} = Install.stage_1_preflight(deps)
      assert report.package_manager == nil
    end
  end

  describe "stage_2_os_packages/1" do
    test "skips when libvips already installed" do
      report = %{installed: %{libvips: true}, package_manager: :brew}
      deps = %{run_cmd: fn _, _, _ -> raise "should not run" end}
      assert :ok = Install.stage_2_os_packages(report, deps)
    end

    test "runs brew install vips when missing on Mac" do
      report = %{installed: %{libvips: false}, package_manager: :brew}
      deps = %{run_cmd: fn cmd, args, _opts ->
        send(self(), {:cmd, cmd, args})
        {"==> Pouring vips...", 0}
      end}
      assert :ok = Install.stage_2_os_packages(report, deps)
      assert_received {:cmd, "brew", ["install", "vips"]}
    end

    test "errors with suggestion on apt without sudo (we don't escalate)" do
      report = %{installed: %{libvips: false}, package_manager: :apt}
      deps = %{run_cmd: fn _, _, _ -> raise "should not run" end}
      assert {:error, %{stage: :os_packages, suggestion: suggestion}} =
        Install.stage_2_os_packages(report, deps)
      assert suggestion =~ "sudo apt"
    end

    test "errors with no package manager" do
      report = %{installed: %{libvips: false}, package_manager: nil}
      deps = %{}
      assert {:error, %{stage: :os_packages}} = Install.stage_2_os_packages(report, deps)
    end

    test "errors when brew install fails" do
      report = %{installed: %{libvips: false}, package_manager: :brew}
      deps = %{run_cmd: fn _, _, _ -> {"could not download bottle", 1} end}
      assert {:error, %{stage: :os_packages, reason: reason}} =
        Install.stage_2_os_packages(report, deps)
      assert reason =~ "brew install vips failed"
    end
  end

  describe "stage_3_docker_check/1" do
    test "ok when docker info returns 0" do
      deps = %{run_cmd: fn "docker", ["info"], _ -> {"Server Version: 24.0.6", 0} end}
      assert :ok = Install.stage_3_docker_check(deps)
    end

    test "errors with install instructions when docker missing" do
      deps = %{run_cmd: fn "docker", _, _ -> {"command not found", 127} end}
      assert {:error, %{stage: :docker_check, suggestion: s}} = Install.stage_3_docker_check(deps)
      assert s =~ "Install Docker"
    end

    test "errors when daemon not running" do
      deps = %{run_cmd: fn "docker", _, _ -> {"Cannot connect to the Docker daemon", 1} end}
      assert {:error, %{stage: :docker_check, reason: reason}} = Install.stage_3_docker_check(deps)
      assert reason =~ "Cannot connect"
    end
  end

  describe "stage_4_data_directory/1" do
    setup do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      on_exit(fn -> File.rm_rf!(tmp) end)
      {:ok, tmp: tmp}
    end

    test "creates the directory tree", %{tmp: tmp} do
      deps = %{
        data_dir: fn -> tmp end,
        mkdir_p: &File.mkdir_p/1,
        chmod: &File.chmod/2
      }
      assert :ok = Install.stage_4_data_directory(deps)
      assert File.dir?(Path.join(tmp, "services/pg_data"))
      assert File.dir?(Path.join(tmp, "services/minio_data"))
    end

    test "sets mode 0700 on data dir", %{tmp: tmp} do
      deps = %{data_dir: fn -> tmp end, mkdir_p: &File.mkdir_p/1, chmod: &File.chmod/2}
      assert :ok = Install.stage_4_data_directory(deps)
      mode = File.stat!(tmp).mode |> rem(0o1000)
      assert mode == 0o700
    end

    test "is idempotent on re-run", %{tmp: tmp} do
      deps = %{data_dir: fn -> tmp end, mkdir_p: &File.mkdir_p/1, chmod: &File.chmod/2}
      assert :ok = Install.stage_4_data_directory(deps)
      assert :ok = Install.stage_4_data_directory(deps)
    end
  end

  describe "stage_5_secrets/1" do
    setup do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      File.mkdir_p!(tmp)
      on_exit(fn -> File.rm_rf!(tmp) end)
      {:ok, tmp: tmp}
    end

    test "writes secrets.exs with secret_key_base", %{tmp: tmp} do
      deps = %{
        data_dir: fn -> tmp end,
        file_exists: &File.exists?/1,
        file_write: &File.write/2,
        chmod: &File.chmod/2
      }
      assert :ok = Install.stage_5_secrets(deps)
      content = File.read!(Path.join(tmp, "secrets.exs"))
      assert content =~ "secret_key_base:"
      assert content =~ "import Config"
    end

    test "preserves existing secrets.exs", %{tmp: tmp} do
      File.write!(Path.join(tmp, "secrets.exs"), "# preserved")
      deps = %{
        data_dir: fn -> tmp end,
        file_exists: &File.exists?/1,
        file_write: fn _, _ -> raise "should not write" end,
        chmod: &File.chmod/2
      }
      assert :ok = Install.stage_5_secrets(deps)
      assert File.read!(Path.join(tmp, "secrets.exs")) == "# preserved"
    end

    test "sets secrets.exs to mode 0600", %{tmp: tmp} do
      deps = %{
        data_dir: fn -> tmp end,
        file_exists: &File.exists?/1,
        file_write: &File.write/2,
        chmod: &File.chmod/2
      }
      assert :ok = Install.stage_5_secrets(deps)
      mode = File.stat!(Path.join(tmp, "secrets.exs")).mode |> rem(0o1000)
      assert mode == 0o600
    end
  end

  describe "stage_6_config/1" do
    setup do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      File.mkdir_p!(tmp)
      on_exit(fn -> File.rm_rf!(tmp) end)
      {:ok, tmp: tmp}
    end

    test "writes config.exs from template", %{tmp: tmp} do
      deps = %{
        data_dir: fn -> tmp end,
        file_exists: &File.exists?/1,
        file_write: &File.write/2
      }
      assert :ok = Install.stage_6_config(deps)
      content = File.read!(Path.join(tmp, "config.exs"))
      assert content =~ "config :boxland, Boxland.Repo"
      assert content =~ "import_config \"secrets.exs\""
    end

    test "preserves existing config.exs", %{tmp: tmp} do
      File.write!(Path.join(tmp, "config.exs"), "# preserved")
      deps = %{
        data_dir: fn -> tmp end,
        file_exists: &File.exists?/1,
        file_write: fn _, _ -> raise "should not write" end
      }
      assert :ok = Install.stage_6_config(deps)
      assert File.read!(Path.join(tmp, "config.exs")) == "# preserved"
    end
  end

  describe "stage_7_compose/1" do
    setup do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      File.mkdir_p!(Path.join(tmp, "services"))
      on_exit(fn -> File.rm_rf!(tmp) end)
      {:ok, tmp: tmp}
    end

    test "always overwrites docker-compose.yml (deterministic from template)", %{tmp: tmp} do
      File.write!(Path.join(tmp, "services/docker-compose.yml"), "# stale")
      deps = %{
        data_dir: fn -> tmp end,
        file_write: &File.write/2
      }
      assert :ok = Install.stage_7_compose(deps)
      content = File.read!(Path.join(tmp, "services/docker-compose.yml"))
      assert content =~ "image: postgres:16"
      assert content =~ tmp <> "/services/pg_data"
      refute content =~ "stale"
    end
  end

  describe "stage_8_services/1" do
    test "ok when compose up succeeds and services healthy" do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      File.mkdir_p!(tmp)
      on_exit(fn -> File.rm_rf!(tmp) end)

      deps = %{
        data_dir: fn -> tmp end,
        run_cmd: fn
          "docker", ["compose", "-f", _, "up", "-d"], _ -> {"Started", 0}
          "docker", ["compose", "-f", _, "ps", "--format", "json"], _ ->
            {~s({"Service":"postgres","Health":"healthy"}\n{"Service":"redis","Health":"healthy"}\n{"Service":"minio","Health":""}), 0}
        end,
        sleep: fn _ -> :ok end
      }
      # mkdir the services subdir
      File.mkdir_p!(Path.join(tmp, "services"))
      File.write!(Path.join(tmp, "services/docker-compose.yml"), "")

      assert :ok = Install.stage_8_services(deps)
    end

    test "errors when docker compose up fails" do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      File.mkdir_p!(Path.join(tmp, "services"))
      File.write!(Path.join(tmp, "services/docker-compose.yml"), "")
      on_exit(fn -> File.rm_rf!(tmp) end)

      deps = %{
        data_dir: fn -> tmp end,
        run_cmd: fn "docker", ["compose", "-f", _, "up", "-d"], _ -> {"port already in use", 1} end,
        sleep: fn _ -> :ok end
      }
      assert {:error, %{stage: :services, reason: reason}} = Install.stage_8_services(deps)
      assert reason =~ "port already in use"
    end

    test "errors when health poll times out" do
      tmp = Path.join(System.tmp_dir!(), "boxland-test-#{System.unique_integer([:positive])}")
      File.mkdir_p!(Path.join(tmp, "services"))
      File.write!(Path.join(tmp, "services/docker-compose.yml"), "")
      on_exit(fn -> File.rm_rf!(tmp) end)

      deps = %{
        data_dir: fn -> tmp end,
        run_cmd: fn
          "docker", ["compose", "-f", _, "up", "-d"], _ -> {"Started", 0}
          "docker", ["compose", "-f", _, "ps", "--format", "json"], _ ->
            {~s({"Service":"postgres","Health":"starting"}), 0}
        end,
        sleep: fn _ -> :ok end,
        max_health_polls: 3   # short cycle for tests
      }
      assert {:error, %{stage: :services, reason: reason}} = Install.stage_8_services(deps)
      assert reason =~ "Timeout"
    end
  end
end
