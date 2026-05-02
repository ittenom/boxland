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
end
