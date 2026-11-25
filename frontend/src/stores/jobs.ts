import { defineStore } from "pinia";
import { computed, ref } from "vue";
import * as api from "@/api/jobs";
import type { Job } from "@/api/jobs";
import { StatusError } from "@/api/utils";

// A job only appears in the panel once it has been running this long, so
// quick moves and renames don't flash it on screen.
const SHOW_AFTER_MS = 800;
// How long a successful job stays in the panel before it is removed.
const DONE_LINGER_MS = 4000;
// Polling starts fast so short jobs settle quickly, then backs off.
const MIN_POLL_MS = 150;
const MAX_POLL_MS = 1000;

type Waiter = {
  resolve: (job: Job) => void;
  reject: (err: unknown) => void;
};

export const useJobsStore = defineStore("jobs", () => {
  const jobs = ref<Job[]>([]);
  const visible = ref<Set<string>>(new Set());

  const firstSeen = new Map<string, number>();
  const dismissed = new Set<string>();
  const lingering = new Set<string>();
  const waiters = new Map<string, Waiter[]>();

  let timer: ReturnType<typeof setTimeout> | null = null;
  let delay = MIN_POLL_MS;

  const panelJobs = computed(() =>
    jobs.value.filter((j) => visible.value.has(j.id))
  );
  const activeCount = computed(
    () => panelJobs.value.filter((j) => !api.isTerminal(j.status)).length
  );
  const finishedCount = computed(
    () => panelJobs.value.filter((j) => api.isTerminal(j.status)).length
  );

  function schedule(reset = false) {
    if (reset) {
      delay = MIN_POLL_MS;
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
    }
    if (timer !== null) return;
    timer = setTimeout(poll, delay);
    delay = Math.min(delay * 2, MAX_POLL_MS);
  }

  function upsert(job: Job) {
    const i = jobs.value.findIndex((j) => j.id === job.id);
    if (i === -1) {
      jobs.value.unshift(job);
    } else {
      jobs.value[i] = job;
    }
  }

  function settle(job: Job) {
    const list = waiters.get(job.id);
    if (!list) return;
    waiters.delete(job.id);
    list.forEach((w) => w.resolve(job));
  }

  function rejectAll(err: unknown) {
    for (const list of waiters.values()) {
      list.forEach((w) => w.reject(err));
    }
    waiters.clear();
  }

  function observe(list: Job[]) {
    const now = Date.now();
    jobs.value = list;

    for (const job of list) {
      if (!firstSeen.has(job.id)) firstSeen.set(job.id, now);
      if (dismissed.has(job.id)) continue;

      const terminal = api.isTerminal(job.status);
      if (!terminal && now - firstSeen.get(job.id)! >= SHOW_AFTER_MS) {
        visible.value.add(job.id);
      }

      if (terminal) {
        settle(job);
        if (
          job.status === "done" &&
          visible.value.has(job.id) &&
          !lingering.has(job.id)
        ) {
          lingering.add(job.id);
          setTimeout(() => dismiss(job.id), DONE_LINGER_MS);
        }
      }
    }

    // A job we are waiting on is gone from the server (it restarted, or the
    // job was pruned): stop waiting rather than hang forever.
    for (const id of [...waiters.keys()]) {
      if (!list.some((j) => j.id === id)) {
        const stale = waiters.get(id)!;
        waiters.delete(id);
        stale.forEach((w) =>
          w.reject(new StatusError("404 Task no longer exists", 404))
        );
      }
    }
  }

  async function poll() {
    timer = null;
    try {
      observe(await api.list());
    } catch (e) {
      if (e instanceof StatusError && e.status === 401) {
        rejectAll(e);
        return;
      }
      // Transient error: keep trying while anything is pending.
    }

    const pending = jobs.value.some((j) => !api.isTerminal(j.status));
    if (pending || waiters.size > 0) schedule();
  }

  /** Start following a job that was just submitted. */
  function track(job: Job) {
    firstSeen.set(job.id, Date.now());
    upsert(job);
    schedule(true);
  }

  /** Resolve once the job reaches a final state. */
  function wait(id: string): Promise<Job> {
    const known = jobs.value.find((j) => j.id === id);
    if (known && api.isTerminal(known.status)) {
      return Promise.resolve(known);
    }
    return new Promise((resolve, reject) => {
      const list = waiters.get(id) ?? [];
      list.push({ resolve, reject });
      waiters.set(id, list);
      schedule();
    });
  }

  async function cancel(id: string) {
    await api.cancel(id);
    schedule(true);
  }

  function dismiss(id: string) {
    dismissed.add(id);
    visible.value.delete(id);
  }

  function clearFinished() {
    for (const job of panelJobs.value) {
      if (api.isTerminal(job.status)) dismiss(job.id);
    }
  }

  /** Pick up jobs that were already running, e.g. after a page reload. */
  async function init() {
    try {
      const list = await api.list();
      for (const job of list) {
        // Already running before this page loaded: show it straight away.
        firstSeen.set(job.id, 0);
        if (api.isTerminal(job.status)) dismissed.add(job.id);
      }
      observe(list);
      if (list.some((j) => !api.isTerminal(j.status))) schedule(true);
    } catch {
      // Not fatal: the panel simply starts empty.
    }
  }

  return {
    jobs,
    panelJobs,
    activeCount,
    finishedCount,
    track,
    wait,
    cancel,
    dismiss,
    clearFinished,
    init,
  };
});
