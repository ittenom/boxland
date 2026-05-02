defmodule Boxland.TUI.MenuTest do
  use ExUnit.Case, async: true
  alias Boxland.TUI.Menu

  describe "items/2" do
    test "pre-install: Install featured, Run Server disabled, Quit" do
      items = Menu.items(installed_at: nil, server_status: :stopped)
      assert length(items) == 3
      assert Enum.at(items, 0).id == :install
      assert Enum.at(items, 0).style == :featured
      assert Enum.at(items, 1).id == :run_server
      assert Enum.at(items, 1).disabled?
      assert Enum.at(items, 2).id == :quit
    end

    test "post-install, server stopped: Run Server featured, Re-check demoted, Quit" do
      items = Menu.items(installed_at: ~U[2026-05-01 12:00:00Z], server_status: :stopped)
      assert length(items) == 3
      assert Enum.at(items, 0).id == :run_server
      assert Enum.at(items, 0).style == :featured
      refute Enum.at(items, 0).disabled?
      assert Enum.at(items, 1).id == :recheck_install
      assert Enum.at(items, 1).style == :demoted
      assert Enum.at(items, 2).id == :quit
    end

    test "post-install, server running: Stop Server featured, Re-check disabled, Quit" do
      items = Menu.items(installed_at: ~U[2026-05-01 12:00:00Z], server_status: :running)
      assert length(items) == 3
      assert Enum.at(items, 0).id == :stop_server
      assert Enum.at(items, 0).style == :featured
      assert Enum.at(items, 1).id == :recheck_install
      assert Enum.at(items, 1).disabled?
      assert Enum.at(items, 2).id == :quit
    end

    test "upgrade pending: Re-check Install gets featured priority over Run Server" do
      items =
        Menu.items(
          installed_at: ~U[2026-05-01 12:00:00Z],
          server_status: :stopped,
          upgrade_pending: true
        )

      assert Enum.at(items, 0).id == :recheck_install
      assert Enum.at(items, 0).style == :featured
      assert Enum.at(items, 1).id == :run_server
      assert Enum.at(items, 1).style == :demoted
    end
  end

  describe "next_selectable/3" do
    test "skips disabled items moving down" do
      items = Menu.items(installed_at: nil, server_status: :stopped)
      # Index 0 = Install (featured), 1 = Run Server (disabled), 2 = Quit
      assert Menu.next_selectable(items, 0, :down) == 2
      # wraps
      assert Menu.next_selectable(items, 2, :down) == 0
    end

    test "skips disabled items moving up" do
      items = Menu.items(installed_at: nil, server_status: :stopped)
      assert Menu.next_selectable(items, 2, :up) == 0
      # wraps
      assert Menu.next_selectable(items, 0, :up) == 2
    end
  end

  describe "upgrade_pending?/2" do
    test "true when versions differ" do
      assert Menu.upgrade_pending?("0.1.0", "0.2.0")
    end

    test "false when versions match" do
      refute Menu.upgrade_pending?("0.1.0", "0.1.0")
    end

    test "false when marker version is nil" do
      refute Menu.upgrade_pending?(nil, "0.1.0")
    end
  end
end
