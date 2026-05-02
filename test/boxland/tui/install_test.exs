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
end
