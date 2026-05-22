defmodule Boxland.Storage do
  @moduledoc """
  Object-storage backend for user-uploaded assets (Cloudflare R2 via the S3 API).

  Configuration (set via env vars in config/runtime.exs):

    * `S3_HOST`               — e.g. `<account_id>.r2.cloudflarestorage.com`
    * `S3_BUCKET`             — bucket name (e.g. `boxland-tiles`)
    * `S3_ACCESS_KEY_ID`      — R2 access key
    * `S3_SECRET_ACCESS_KEY`  — R2 secret
    * `CDN_BASE_URL`          — public URL prefix used to build `content_url`
                                 (e.g. `https://pub-XXXX.r2.dev` or a custom domain)
  """

  def put_object(key, body, content_type) when is_binary(key) and is_binary(body) do
    bucket()
    |> ExAws.S3.put_object(key, body, content_type: content_type)
    |> ExAws.request()
    |> case do
      {:ok, _} -> {:ok, public_url(key)}
      {:error, reason} -> {:error, {:s3_upload_failed, reason}}
    end
  end

  def public_url(key) when is_binary(key) do
    cdn_base_url()
    |> String.trim_trailing("/")
    |> Kernel.<>("/" <> key)
  end

  defp bucket do
    case Application.fetch_env(:ex_aws, :s3) do
      {:ok, opts} ->
        Keyword.get(opts, :bucket) ||
          raise "S3_BUCKET is not configured (set the S3_BUCKET env var)"

      :error ->
        raise "ex_aws :s3 config missing (set S3_HOST/S3_BUCKET env vars)"
    end
  end

  defp cdn_base_url do
    case Application.fetch_env(:boxland, :cdn_base_url) do
      {:ok, url} when is_binary(url) and url != "" -> url
      _ -> raise "CDN_BASE_URL is not configured"
    end
  end
end
