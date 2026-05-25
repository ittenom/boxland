defmodule Boxland.Repo.Migrations.ChangeEntityTypeDefaultZIndexToZero do
  use Ecto.Migration

  def up do
    alter table(:entity_types) do
      modify :default_z_index, :integer, null: false, default: 0
    end
  end

  def down do
    alter table(:entity_types) do
      modify :default_z_index, :integer, null: false, default: 25
    end
  end
end
