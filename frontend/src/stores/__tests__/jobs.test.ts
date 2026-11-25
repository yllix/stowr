import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import type { Job } from "@/api/jobs";

const listMock = vi.fn<() => Promise<Job[]>>();
const cancelMock = vi.fn<(id: string) => Promise<void>>();

vi.mock("@/api/jobs", () => ({
  list: () => listMock(),
  cancel: (id: string) => cancelMock(id),
  isTerminal: (s: string) => s === "done" || s === "failed" || s === "canceled",
}));

vi.mock("@/api/utils", () => ({
  StatusError: class StatusError extends Error {
    constructor(
      message: string,
      public status?: number
    ) {
      super(message);
    }
  },
}));

import { useJobsStore } from "@/stores/jobs";

function job(id: string, status: Job["status"], extra: Partial<Job> = {}): Job {
  return {
    id,
    action: "copy",
    src: "/a",
    dst: "/b",
    status,
    totalBytes: 100,
    doneBytes: 0,
    totalFiles: 1,
    doneFiles: 0,
    created: new Date().toISOString(),
    ...extra,
  };
}

describe("jobs store", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setActivePinia(createPinia());
    listMock.mockReset();
    cancelMock.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("resolves wait() once the job finishes", async () => {
    const store = useJobsStore();
    listMock
      .mockResolvedValueOnce([job("j1", "running", { doneBytes: 50 })])
      .mockResolvedValueOnce([job("j1", "done", { doneBytes: 100 })]);

    store.track(job("j1", "queued"));
    const done = store.wait("j1");

    await vi.advanceTimersByTimeAsync(2000);
    await expect(done).resolves.toMatchObject({ id: "j1", status: "done" });
  });

  it("reports failures through the resolved job", async () => {
    const store = useJobsStore();
    listMock.mockResolvedValue([
      job("j1", "failed", { error: "permission denied" }),
    ]);

    store.track(job("j1", "queued"));
    const done = store.wait("j1");

    await vi.advanceTimersByTimeAsync(500);
    await expect(done).resolves.toMatchObject({
      status: "failed",
      error: "permission denied",
    });
  });

  it("rejects waiters when the job disappears from the server", async () => {
    const store = useJobsStore();
    listMock.mockResolvedValue([]);

    store.track(job("gone", "queued"));
    const done = store.wait("gone");
    const assertion = expect(done).rejects.toThrow("no longer exists");

    await vi.advanceTimersByTimeAsync(500);
    await assertion;
  });

  it("keeps quick jobs out of the panel", async () => {
    const store = useJobsStore();
    listMock.mockResolvedValue([job("fast", "done")]);

    store.track(job("fast", "queued"));
    const done = store.wait("fast");
    await vi.advanceTimersByTimeAsync(500);
    await done;

    expect(store.panelJobs).toHaveLength(0);
  });

  it("shows long-running jobs in the panel", async () => {
    const store = useJobsStore();
    listMock.mockResolvedValue([job("slow", "running")]);

    store.track(job("slow", "queued"));
    await vi.advanceTimersByTimeAsync(3000);

    expect(store.panelJobs.map((j) => j.id)).toEqual(["slow"]);
    expect(store.activeCount).toBe(1);
  });

  it("shows jobs already running when the page loads", async () => {
    const store = useJobsStore();
    listMock.mockResolvedValue([job("old", "running"), job("prev", "done")]);

    await store.init();

    expect(store.panelJobs.map((j) => j.id)).toEqual(["old"]);
  });
});
