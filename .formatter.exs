[
  import_deps: [:ecto, :ecto_sql, :phoenix],
  subdirectories: ["priv/*/migrations"],
  plugins: [Phoenix.LiveView.HTMLFormatter],
  inputs: ["*.{heex,ex,exs}", "{config,lib,test}/**/*.{heex,ex,exs}", "priv/*/seeds.exs"]
    |> Enum.flat_map(fn pat -> Path.wildcard(pat) end)
    |> Enum.reject(&String.ends_with?(&1, ".pb.ex"))
]
