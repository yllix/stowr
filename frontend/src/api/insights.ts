import { fetchURL, fetchJSON, removePrefix } from "./utils";

export type IndexState = "building" | "ready" | "too_large" | "disabled";

export interface IndexStatus {
  state: IndexState;
  entries: number;
  builtAt?: string;
  duration?: string;
  building: boolean;
}

export interface UsageItem {
  path: string;
  name: string;
  isDir: boolean;
  size: number;
  files: number;
  modified: string;
}

export interface Insights {
  status: IndexStatus;
  path: string;
  usage?: {
    size: number;
    files: number;
    children: UsageItem[] | null;
    builtAt: string;
  };
}

/** Folder sizes for a route path such as "/storage/Lab_Share/". */
export async function get(url: string): Promise<Insights> {
  return fetchJSON<Insights>(`/api/insights${removePrefix(url)}`, {});
}

/** Ask the server to rebuild the file index (admins only). */
export async function refresh(): Promise<void> {
  await fetchURL(`/api/index/refresh`, { method: "POST" });
}

export interface DuplicateGroup {
  size: number;
  hash: string;
  paths: string[];
}

export interface DuplicatesResult {
  path: string;
  minSize: number;
  groups: DuplicateGroup[];
  wasted: number;
  truncated?: boolean;
}

/** Start a duplicate scan of a folder; resolves with the queued job. */
export async function findDuplicates(url: string, minSize: number) {
  const res = await fetchURL(
    `/api/duplicates${removePrefix(url)}?minSize=${minSize}`,
    { method: "POST" }
  );
  return res.json();
}
