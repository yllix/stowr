<template>
  <div>
    <header-bar showMenu showLogo>
      <title />
      <action
        icon="folder_open"
        :label="t('storage.openInFiles')"
        @action="openInFiles"
      />
      <action
        v-if="isAdmin"
        icon="sync"
        :label="t('storage.rescan')"
        @action="rescan"
      />
    </header-bar>

    <breadcrumbs base="/storage" />

    <div class="storage">
      <div v-if="loading && !data" class="storage-message">
        <i class="material-icons spin">autorenew</i>
        {{ t("storage.loading") }}
      </div>

      <div v-else-if="error" class="storage-message error">
        <i class="material-icons">error_outline</i>
        {{ error }}
      </div>

      <div
        v-else-if="data && data.status.state !== 'ready'"
        class="storage-message"
      >
        <i class="material-icons">{{ stateIcon }}</i>
        <div>
          <p class="storage-message-title">{{ stateTitle }}</p>
          <p>{{ stateBody }}</p>
        </div>
      </div>

      <template v-else-if="data && data.usage">
        <section class="storage-summary">
          <div class="stat">
            <span class="stat-label">{{ t("storage.total") }}</span>
            <span class="stat-value">{{ size(data.usage.size) }}</span>
          </div>
          <div class="stat">
            <span class="stat-label">{{ t("storage.files") }}</span>
            <span class="stat-value">{{ count(data.usage.files) }}</span>
          </div>
          <div class="stat">
            <span class="stat-label">{{ t("storage.items") }}</span>
            <span class="stat-value">{{ count(children.length) }}</span>
          </div>
          <div class="stat stat-muted">
            <span class="stat-label">{{ t("storage.indexed") }}</span>
            <span class="stat-value small">
              {{ builtAgo }}
              <i
                v-if="data.status.building"
                class="material-icons spin"
                :title="t('storage.rebuilding')"
                >autorenew</i
              >
            </span>
          </div>
        </section>

        <div v-if="children.length === 0" class="storage-message">
          <i class="material-icons">folder_off</i>
          {{ t("storage.empty") }}
        </div>

        <ul v-else class="storage-list">
          <li
            v-for="item in visible"
            :key="item.name"
            :class="{ dir: item.isDir }"
            :tabindex="0"
            @click="open(item)"
            @keydown.enter="open(item)"
          >
            <i class="material-icons icon">{{
              item.isDir ? "folder" : "insert_drive_file"
            }}</i>
            <div class="row">
              <div class="row-top">
                <span class="name" :title="item.name">{{ item.name }}</span>
                <span class="size">{{ size(item.size) }}</span>
              </div>
              <div class="bar">
                <div :style="{ width: barWidth(item) + '%' }"></div>
              </div>
              <div class="row-bottom">
                <span>{{ percent(item) }}</span>
                <span v-if="item.isDir">{{
                  t("storage.fileCount", { count: count(item.files) })
                }}</span>
              </div>
            </div>
            <i v-if="item.isDir" class="material-icons chevron"
              >chevron_right</i
            >
          </li>
        </ul>

        <button
          v-if="children.length > visible.length"
          class="button button--flat storage-more"
          @click="showAll = true"
        >
          {{ t("storage.showAll", { count: children.length }) }}
        </button>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { partial } from "filesize";
import dayjs from "dayjs";
import HeaderBar from "@/components/header/HeaderBar.vue";
import Action from "@/components/header/Action.vue";
import Breadcrumbs from "@/components/Breadcrumbs.vue";
import { insights as api } from "@/api";
import type { Insights, UsageItem } from "@/api/insights";
import { useAuthStore } from "@/stores/auth";
import { StatusError } from "@/api/utils";

const SHOW_FIRST = 200;

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();

const data = ref<Insights | null>(null);
const loading = ref(false);
const error = ref("");
const showAll = ref(false);

const size = partial({ base: 2 });
const isAdmin = computed(() => !!authStore.user?.perm.admin);

const children = computed<UsageItem[]>(() =>
  [...(data.value?.usage?.children ?? [])].sort((a, b) => b.size - a.size)
);
const visible = computed(() =>
  showAll.value ? children.value : children.value.slice(0, SHOW_FIRST)
);
const largest = computed(() => children.value[0]?.size ?? 0);

const builtAgo = computed(() =>
  data.value?.usage?.builtAt ? dayjs(data.value.usage.builtAt).fromNow() : "—"
);

const stateIcon = computed(() => {
  switch (data.value?.status.state) {
    case "building":
      return "hourglass_top";
    case "too_large":
      return "warning";
    default:
      return "info";
  }
});
const stateTitle = computed(() =>
  t(`storage.state.${data.value?.status.state ?? "building"}.title`)
);
const stateBody = computed(() =>
  t(`storage.state.${data.value?.status.state ?? "building"}.body`)
);

function count(n: number): string {
  return n.toLocaleString();
}

function barWidth(item: UsageItem): number {
  if (largest.value <= 0) return 0;
  return Math.max(0.5, (item.size / largest.value) * 100);
}

function percent(item: UsageItem): string {
  const total = data.value?.usage?.size ?? 0;
  if (total <= 0) return "0%";
  const p = (item.size / total) * 100;
  return p < 0.1 && p > 0 ? "<0.1%" : `${p.toFixed(1)}%`;
}

function basePath(): string {
  return route.path.endsWith("/") ? route.path : route.path + "/";
}

function open(item: UsageItem) {
  const name = encodeURIComponent(item.name);
  if (item.isDir) {
    router.push({ path: basePath() + name + "/" });
  } else {
    router.push({
      path: basePath().replace(/^\/storage/, "/files") + name,
    });
  }
}

function openInFiles() {
  router.push({ path: basePath().replace(/^\/storage/, "/files") });
}

async function load() {
  loading.value = true;
  error.value = "";
  showAll.value = false;
  try {
    data.value = await api.get(route.path);
    if (data.value.status.state === "building") {
      // Try again shortly while the first index build runs.
      setTimeout(() => {
        if (route.path.startsWith("/storage")) load();
      }, 3000);
    }
  } catch (e) {
    data.value = null;
    error.value =
      e instanceof StatusError && e.status === 404
        ? t("storage.notIndexed")
        : (e as Error).message;
  } finally {
    loading.value = false;
  }
}

async function rescan() {
  await api.refresh();
  if (data.value) data.value.status.building = true;
}

watch(() => route.path, load, { immediate: true });
</script>

<style scoped>
.storage {
  padding: 0.5em 0 2em;
}

.storage-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(10em, 1fr));
  gap: 0.75em;
  margin-bottom: 1em;
}

.stat {
  background: var(--surfacePrimary);
  border: 1px solid var(--borderPrimary);
  border-radius: var(--radius-lg, 14px);
  padding: 0.9em 1.1em;
  display: flex;
  flex-direction: column;
  gap: 0.25em;
  box-shadow: var(--shadow-sm, none);
}

.stat-label {
  font-size: 0.75em;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--textPrimary);
  font-weight: 600;
}

.stat-value {
  font-size: 1.5em;
  font-weight: 700;
  color: var(--textSecondary);
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
}

.stat-value.small {
  font-size: 1em;
  font-weight: 500;
  display: flex;
  align-items: center;
  gap: 0.3em;
}

.stat-value.small i {
  font-size: 1.1em;
  color: var(--accent, var(--blue));
}

.storage-list {
  list-style: none;
  margin: 0;
  padding: 0;
  background: var(--surfacePrimary);
  border: 1px solid var(--borderPrimary);
  border-radius: var(--radius-lg, 14px);
  box-shadow: var(--shadow-sm, none);
  overflow: hidden;
}

.storage-list li {
  display: flex;
  align-items: center;
  gap: 0.9em;
  padding: 0.75em 1em;
  border-bottom: 1px solid var(--divider);
  cursor: pointer;
  transition: 0.1s ease background;
}

.storage-list li:last-child {
  border-bottom: 0;
}

.storage-list li:hover,
.storage-list li:focus-visible {
  background: var(--hover);
  outline: none;
}

.storage-list .icon {
  font-size: 1.75em;
  color: var(--textPrimary);
}

.storage-list li.dir .icon {
  color: var(--iconPrimary);
}

.storage-list .row {
  flex: 1;
  min-width: 0;
}

.storage-list .row-top,
.storage-list .row-bottom {
  display: flex;
  justify-content: space-between;
  gap: 1em;
}

.storage-list .name {
  font-weight: 500;
  color: var(--textSecondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-list .size {
  font-weight: 600;
  color: var(--textSecondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.storage-list .row-bottom {
  font-size: 0.8em;
  color: var(--textPrimary);
  font-variant-numeric: tabular-nums;
}

.storage-list .bar {
  height: 6px;
  margin: 0.45em 0 0.35em;
  border-radius: 999px;
  background: var(--surfaceSecondary);
  overflow: hidden;
}

.storage-list .bar div {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(
    90deg,
    var(--brand-600, var(--blue)),
    var(--brand-400, var(--blue))
  );
  transition: 0.3s ease width;
}

.storage-list li:not(.dir) .bar div {
  background: var(--borderSecondary);
}

.storage-list .chevron {
  color: var(--textPrimary);
}

.storage-message {
  display: flex;
  align-items: center;
  gap: 0.75em;
  padding: 1.25em 1.5em;
  background: var(--surfacePrimary);
  border: 1px solid var(--borderPrimary);
  border-radius: var(--radius-lg, 14px);
  color: var(--textPrimary);
}

.storage-message p {
  margin: 0.15em 0;
}

.storage-message-title {
  font-weight: 600;
  color: var(--textSecondary);
}

.storage-message.error {
  color: var(--red);
}

.storage-more {
  display: block;
  margin: 1em auto 0;
}
</style>
