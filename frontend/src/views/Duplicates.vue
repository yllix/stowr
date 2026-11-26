<template>
  <div>
    <header-bar showMenu showLogo>
      <title />
      <action
        icon="donut_large"
        :label="t('sidebar.storage')"
        @action="toStorage"
      />
    </header-bar>

    <breadcrumbs base="/duplicates" />

    <div class="dupes">
      <section class="dupes-controls">
        <div class="dupes-intro">
          <h2>{{ t("duplicates.title") }}</h2>
          <p>{{ t("duplicates.intro") }}</p>
        </div>
        <div class="dupes-actions">
          <label>
            <span>{{ t("duplicates.minSize") }}</span>
            <select v-model.number="minSize" class="input" :disabled="scanning">
              <option v-for="o in sizeOptions" :key="o" :value="o">
                {{ size(o) }}
              </option>
            </select>
          </label>
          <button class="button" :disabled="scanning" @click="scan">
            <i class="material-icons">find_in_page</i>
            {{ result ? t("duplicates.rescan") : t("duplicates.scan") }}
          </button>
        </div>
      </section>

      <div v-if="scanning" class="dupes-message">
        <i class="material-icons spin">autorenew</i>
        {{ t("duplicates.scanning") }}
      </div>

      <div v-else-if="error" class="dupes-message error">
        <i class="material-icons">error_outline</i>
        {{ error }}
      </div>

      <template v-else-if="result">
        <div v-if="groups.length === 0" class="dupes-message">
          <i class="material-icons">check_circle</i>
          {{ t("duplicates.none") }}
          <span v-if="lastScan" class="muted">· {{ lastScan }}</span>
        </div>

        <template v-else>
          <div class="dupes-summary">
            <div>
              <strong>{{
                t("duplicates.summary", {
                  groups: groups.length.toLocaleString(),
                  wasted: size(wasted),
                })
              }}</strong>
              <span v-if="lastScan" class="muted"> · {{ lastScan }}</span>
              <p v-if="result.truncated" class="muted">
                {{ t("duplicates.truncated", { count: groups.length }) }}
              </p>
            </div>
            <div class="dupes-bulk">
              <button class="button button--flat" @click="selectAllButFirst">
                {{ t("duplicates.keepOne") }}
              </button>
              <button
                class="button button--red"
                :disabled="selected.size === 0 || deleting"
                @click="deleteSelected"
              >
                <i class="material-icons">delete</i>
                {{ t("duplicates.deleteSelected") }}
                <span v-if="selected.size">({{ selected.size }})</span>
              </button>
            </div>
          </div>

          <article
            v-for="g in visibleGroups"
            :key="g.hash"
            class="dupe-group"
            :class="{ invalid: allSelected(g) }"
          >
            <div class="group-head">
              <span class="badge">{{
                t("duplicates.copies", { count: g.paths.length })
              }}</span>
              <span class="muted">{{
                t("duplicates.wastedGroup", {
                  size: size(g.size),
                  wasted: size(g.size * (g.paths.length - 1)),
                })
              }}</span>
            </div>
            <ul>
              <li v-for="p in g.paths" :key="p">
                <label>
                  <input
                    type="checkbox"
                    :checked="selected.has(p)"
                    @change="toggle(p)"
                  />
                  <span class="path" :title="p">{{ p }}</span>
                </label>
                <router-link :to="filesLink(p)" class="open">
                  {{ t("duplicates.open") }}
                </router-link>
              </li>
            </ul>
            <p v-if="allSelected(g)" class="warn">
              {{ t("duplicates.keepAtLeastOne") }}
            </p>
          </article>

          <button
            v-if="groups.length > visibleGroups.length"
            class="button button--flat dupes-more"
            @click="limit += 100"
          >
            {{ t("storage.showAll", { count: groups.length }) }}
          </button>
        </template>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { partial } from "filesize";
import dayjs from "dayjs";
import HeaderBar from "@/components/header/HeaderBar.vue";
import Action from "@/components/header/Action.vue";
import Breadcrumbs from "@/components/Breadcrumbs.vue";
import { insights, jobs as jobsApi, files as filesApi } from "@/api";
import type { DuplicateGroup, DuplicatesResult } from "@/api/insights";
import type { Job } from "@/api/jobs";
import { useJobsStore } from "@/stores/jobs";
import { StatusError, removePrefix } from "@/api/utils";

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const jobsStore = useJobsStore();
const $showError = inject<IToastError>("$showError")!;
const $showSuccess = inject<IToastSuccess>("$showSuccess")!;

const size = partial({ base: 2 });
const sizeOptions = [1, 1 << 20, 10 << 20, 100 << 20, 1 << 30];

const minSize = ref(1 << 20);
const scanning = ref(false);
const deleting = ref(false);
const error = ref("");
const result = ref<DuplicatesResult | null>(null);
const finishedAt = ref<string | null>(null);
const groups = ref<DuplicateGroup[]>([]);
const selected = ref(new Set<string>());
const limit = ref(100);

const visibleGroups = computed(() => groups.value.slice(0, limit.value));
const wasted = computed(() =>
  groups.value.reduce((n, g) => n + g.size * (g.paths.length - 1), 0)
);
const lastScan = computed(() =>
  finishedAt.value
    ? t("duplicates.lastScan", { when: dayjs(finishedAt.value).fromNow() })
    : ""
);

// The folder in the user's view, e.g. "/Lab_Share/".
function folder(): string {
  const p = removePrefix(route.path);
  return decodeURIComponent(p.endsWith("/") ? p : p + "/");
}

function encodePath(p: string): string {
  return p.split("/").map(encodeURIComponent).join("/");
}

function filesLink(p: string): string {
  return "/files" + encodePath(p);
}

function toStorage() {
  router.push({ path: route.path.replace(/^\/duplicates/, "/storage") });
}

function show(res: DuplicatesResult, finished?: string) {
  result.value = res;
  finishedAt.value = finished ?? null;
  groups.value = res.groups ?? [];
  selected.value = new Set();
  limit.value = 100;
}

function samePath(a: string, b: string): boolean {
  const norm = (s: string) => (s.endsWith("/") ? s : s + "/");
  return norm(a) === norm(b);
}

// Show the latest finished scan of this folder, if any is still kept.
async function loadLatest() {
  result.value = null;
  error.value = "";
  try {
    const list = await jobsApi.list();
    const latest = list.find(
      (j) =>
        j.action === "duplicates" &&
        j.status === "done" &&
        samePath(j.src, folder())
    );
    if (latest) {
      const full = await jobsApi.get(latest.id);
      if (full.result) show(full.result as DuplicatesResult, full.finished);
    }
  } catch {
    // Nothing to show yet.
  }
}

async function scan() {
  scanning.value = true;
  error.value = "";
  try {
    const job = (await insights.findDuplicates(
      route.path,
      minSize.value
    )) as Job;
    jobsStore.track(job);
    const done = await jobsStore.wait(job.id);
    if (done.status === "failed") {
      error.value = done.error || "operation failed";
      return;
    }
    if (done.status === "canceled") return;
    const full = await jobsApi.get(job.id);
    show(full.result as DuplicatesResult, full.finished);
  } catch (e) {
    error.value =
      e instanceof StatusError && e.status === 503
        ? t("duplicates.needsIndex")
        : (e as Error).message;
  } finally {
    scanning.value = false;
  }
}

function toggle(p: string) {
  const next = new Set(selected.value);
  if (next.has(p)) next.delete(p);
  else next.add(p);
  selected.value = next;
}

function allSelected(g: DuplicateGroup): boolean {
  return g.paths.every((p) => selected.value.has(p));
}

function selectAllButFirst() {
  const next = new Set<string>();
  for (const g of groups.value) {
    g.paths.slice(1).forEach((p) => next.add(p));
  }
  selected.value = next;
}

async function deleteSelected() {
  if (groups.value.some(allSelected)) {
    $showError(new Error(t("duplicates.keepAtLeastOne")));
    return;
  }
  const paths = [...selected.value];
  if (!confirm(t("duplicates.confirmDelete", { count: paths.length }))) {
    return;
  }

  deleting.value = true;
  const removed = new Set<string>();
  try {
    for (const p of paths) {
      await filesApi.remove(filesLink(p));
      removed.add(p);
    }
  } catch (e) {
    $showError(e as Error);
  } finally {
    deleting.value = false;
    groups.value = groups.value
      .map((g) => ({ ...g, paths: g.paths.filter((p) => !removed.has(p)) }))
      .filter((g) => g.paths.length > 1);
    const next = new Set(selected.value);
    removed.forEach((p) => next.delete(p));
    selected.value = next;
    if (removed.size) {
      $showSuccess(t("duplicates.deleted", { count: removed.size }));
    }
  }
}

watch(() => route.path, loadLatest, { immediate: true });
</script>

<style scoped>
.dupes {
  padding: 0.5em 0 2em;
}

.dupes-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
  align-items: flex-end;
  justify-content: space-between;
  background: var(--surfacePrimary);
  border: 1px solid var(--borderPrimary);
  border-radius: var(--radius-lg, 14px);
  padding: 1.1em 1.25em;
  margin-bottom: 1em;
}

.dupes-intro {
  flex: 1 1 22em;
}

.dupes-intro h2 {
  margin: 0 0 0.3em;
  font-size: 1.15em;
  color: var(--textSecondary);
}

.dupes-intro p {
  margin: 0;
  color: var(--textPrimary);
  font-size: 0.9em;
  line-height: 1.45;
}

.dupes-actions {
  display: flex;
  gap: 0.75em;
  align-items: flex-end;
}

.dupes-actions label {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  font-size: 0.8em;
  color: var(--textPrimary);
}

.dupes-actions .button,
.dupes-bulk .button {
  display: inline-flex;
  align-items: center;
  gap: 0.35em;
}

.dupes-actions .button i,
.dupes-bulk .button i {
  font-size: 1.2em;
}

.dupes-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75em;
  align-items: center;
  justify-content: space-between;
  margin: 0 0 1em;
  color: var(--textSecondary);
}

.dupes-summary p {
  margin: 0.25em 0 0;
}

.dupes-bulk {
  display: flex;
  gap: 0.5em;
}

.muted {
  color: var(--textPrimary);
  font-weight: normal;
}

.dupe-group {
  background: var(--surfacePrimary);
  border: 1px solid var(--borderPrimary);
  border-radius: var(--radius-lg, 14px);
  margin-bottom: 0.75em;
  overflow: hidden;
}

.dupe-group.invalid {
  border-color: var(--red);
}

.dupe-group .group-head {
  display: flex;
  gap: 0.75em;
  align-items: center;
  padding: 0.7em 1em;
  border-bottom: 1px solid var(--divider);
  font-size: 0.9em;
}

.badge {
  background: var(--accent-soft, var(--surfaceSecondary));
  color: var(--accent-strong, var(--blue));
  font-weight: 600;
  border-radius: 999px;
  padding: 0.15em 0.65em;
  font-size: 0.85em;
}

.dupe-group ul {
  list-style: none;
  margin: 0;
  padding: 0;
}

.dupe-group li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1em;
  padding: 0.5em 1em;
  border-bottom: 1px solid var(--divider);
}

.dupe-group li:last-child {
  border-bottom: 0;
}

.dupe-group li:hover {
  background: var(--hover);
}

.dupe-group label {
  display: flex;
  align-items: center;
  gap: 0.6em;
  min-width: 0;
  cursor: pointer;
}

.dupe-group input[type="checkbox"] {
  accent-color: var(--accent, var(--blue));
  width: 1.05em;
  height: 1.05em;
}

.dupe-group .path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--textSecondary);
  font-size: 0.92em;
}

.dupe-group .open {
  color: var(--accent, var(--blue));
  font-size: 0.85em;
  white-space: nowrap;
}

.dupe-group .warn {
  margin: 0;
  padding: 0.5em 1em 0.7em;
  color: var(--red);
  font-size: 0.85em;
}

.dupes-message {
  display: flex;
  align-items: center;
  gap: 0.6em;
  padding: 1.1em 1.25em;
  background: var(--surfacePrimary);
  border: 1px solid var(--borderPrimary);
  border-radius: var(--radius-lg, 14px);
  color: var(--textPrimary);
}

.dupes-message.error {
  color: var(--red);
}

.dupes-more {
  display: block;
  margin: 1em auto 0;
}
</style>
