import { fetchJSON, fetchURL } from "./utils";

export type JobStatus = "queued" | "running" | "done" | "failed" | "canceled";

export interface Job {
  id: string;
  action: "copy" | "rename" | "duplicates";
  src: string;
  dst: string;
  status: JobStatus;
  error?: string;
  totalBytes: number;
  doneBytes: number;
  totalFiles: number;
  doneFiles: number;
  created: string;
  started?: string;
  finished?: string;
  result?: unknown;
}

export function isTerminal(status: JobStatus): boolean {
  return status === "done" || status === "failed" || status === "canceled";
}

export async function list(): Promise<Job[]> {
  return fetchJSON<Job[]>(`/api/jobs`, {});
}

export async function get(id: string): Promise<Job> {
  return fetchJSON<Job>(`/api/jobs/${encodeURIComponent(id)}`, {});
}

export async function cancel(id: string): Promise<void> {
  await fetchURL(`/api/jobs/${encodeURIComponent(id)}`, { method: "DELETE" });
}
