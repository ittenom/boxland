defmodule BoxlandWeb.HealthControllerTest do
  use BoxlandWeb.ConnCase, async: true

  describe "GET /healthz" do
    test "returns 200 ok", %{conn: conn} do
      conn = get(conn, ~p"/healthz")
      assert response(conn, 200) == "ok"
    end
  end

  describe "GET /readyz" do
    test "returns 200 ready when db is reachable", %{conn: conn} do
      conn = get(conn, ~p"/readyz")
      assert response(conn, 200) == "ready"
    end
  end
end
