ExUnit.start()
Ecto.Adapters.SQL.Sandbox.mode(Boxland.Repo, :manual)

# The TUI surface refactored Phoenix children out of automatic boot.
# Tests that hit the Endpoint need it running — start it here.
:ok = Boxland.Server.Supervisor.start_children()
