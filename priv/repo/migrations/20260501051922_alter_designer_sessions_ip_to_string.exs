defmodule Boxland.Repo.Migrations.AlterDesignerSessionsIpToString do
  use Ecto.Migration

  def up do
    # Postgrex requires %Postgrex.INET{} for `inet` columns, which doesn't
    # round-trip with Ecto's `:string` field type. Switch to plain varchar
    # to avoid pulling in ecto_network or writing a custom Ecto type.
    execute "ALTER TABLE designer_sessions ALTER COLUMN ip TYPE varchar(45) USING host(ip)"
  end

  def down do
    execute "ALTER TABLE designer_sessions ALTER COLUMN ip TYPE inet USING ip::inet"
  end
end
