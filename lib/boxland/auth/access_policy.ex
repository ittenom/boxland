defmodule Boxland.Auth.AccessPolicy do
  @moduledoc """
  Decides whether a socket (with realm + player_id assigns) may join
  a given Channel topic. Realm isolation is enforced here AND at socket
  connect — defense in depth.
  """

  @type assigns :: %{
          required(:realm) => atom(),
          required(:player_id) => integer(),
          optional(:level_id) => integer()
        }

  @spec allow_join?(assigns, String.t()) :: :ok | {:error, atom()}
  def allow_join?(%{realm: realm} = assigns, "level:" <> rest) do
    case parse_topic(rest) do
      {:ok, parsed} -> check(realm, assigns, parsed)
      :error -> {:error, :bad_topic}
    end
  end

  def allow_join?(_, _topic), do: {:error, :unknown_topic}

  # ---

  defp check(:player, %{player_id: _pid}, {_lvl, :shared}), do: :ok
  defp check(:player, %{player_id: pid}, {_lvl, {:user, uid}}) when pid == uid, do: :ok
  defp check(:player, _, {_lvl, {:user, _}}), do: {:error, :forbidden}

  defp check(:player, %{player_id: _pid}, {_lvl, {:party, _party_id}}) do
    # TODO when parties exist: check membership; for v1 reject
    {:error, :parties_not_implemented}
  end

  defp check(:player, _, {_lvl, {:sandbox, _}}), do: {:error, :forbidden}

  defp check(
         :designer_sandbox,
         %{player_id: did, level_id: my_lvl},
         {topic_lvl, {:sandbox, target_did}}
       )
       when did == target_did and my_lvl == topic_lvl, do: :ok

  defp check(:designer_sandbox, _, _), do: {:error, :forbidden}

  defp check(_, _, _), do: {:error, :forbidden}

  defp parse_topic(rest) do
    case String.split(rest, ":") do
      [lvl, "shared"] ->
        with {lvl_id, ""} <- Integer.parse(lvl), do: {:ok, {lvl_id, :shared}}

      [lvl, "user", uid] ->
        with {lvl_id, ""} <- Integer.parse(lvl),
             {uid_int, ""} <- Integer.parse(uid),
             do: {:ok, {lvl_id, {:user, uid_int}}}

      [lvl, "party", pid] ->
        with {lvl_id, ""} <- Integer.parse(lvl),
             {pid_int, ""} <- Integer.parse(pid),
             do: {:ok, {lvl_id, {:party, pid_int}}}

      [lvl, "sandbox", did] ->
        with {lvl_id, ""} <- Integer.parse(lvl),
             {did_int, ""} <- Integer.parse(did),
             do: {:ok, {lvl_id, {:sandbox, did_int}}}

      _ ->
        :error
    end
  end
end
