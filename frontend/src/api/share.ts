import { fetchURL, fetchJSON, removePrefix, createURL } from "./utils";

export async function list() {
  return fetchJSON<Share[]>("/api/shares");
}

export async function get(url: string) {
  url = removePrefix(url);
  return fetchJSON<Share[]>(`/api/share${url}`);
}

export async function remove(hash: string) {
  await fetchURL(`/api/share/${hash}`, {
    method: "DELETE",
  });
}

export async function create(url: string, options: ShareOptions = {}) {
  url = removePrefix(url);
  return fetchJSON<Share>(`/api/share${url}`, {
    method: "POST",
    body: JSON.stringify({
      password: options.password ?? "",
      // the backend expects a string, and "" for no expiry
      expires: options.expires ? options.expires.toString() : "",
      unit: options.unit ?? "hours",
      note: options.note ?? "",
      maxDownloads: options.maxDownloads ?? 0,
    }),
  });
}

export function getShareURL(share: Share) {
  return createURL("share/" + share.hash, {});
}
