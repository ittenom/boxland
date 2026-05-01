defmodule Boxland.Repo do
  use Ecto.Repo,
    otp_app: :boxland,
    adapter: Ecto.Adapters.Postgres
end
