<template>
  <div
    v-if="jobsStore.panelJobs.length > 0"
    class="upload-files jobs-panel"
    :class="{ closed: !open, stacked: uploadStore.activeUploads.size > 0 }"
  >
    <div class="card floating">
      <div class="card-title">
        <h2>
          {{ $t("jobs.title", { count: jobsStore.activeCount }) }}
        </h2>
        <button
          v-if="jobsStore.finishedCount > 0"
          class="action"
          @click="jobsStore.clearFinished()"
          :aria-label="$t('jobs.clearFinished')"
          :title="$t('jobs.clearFinished')"
        >
          <i class="material-icons">clear_all</i>
        </button>
        <button
          class="action"
          @click="open = !open"
          :aria-label="$t('jobs.toggle')"
          :title="$t('jobs.toggle')"
        >
          <i class="material-icons">{{
            open ? "keyboard_arrow_down" : "keyboard_arrow_up"
          }}</i>
        </button>
      </div>

      <div class="card-content">
        <div
          class="file job"
          v-for="job in jobsStore.panelJobs"
          :key="job.id"
          :aria-label="`${job.src} → ${job.dst}`"
        >
          <div class="file-name">
            <i class="material-icons">{{ actionIcon(job) }}</i>
            <span class="job-name" :title="`${job.src} → ${job.dst}`">
              <template v-if="job.action === 'duplicates'">
                {{ $t("jobs.duplicates") }}
                <span class="job-dest">{{ job.src }}</span>
              </template>
              <template v-else>
                {{ basename(job.src) }}
                <span class="job-dest">→ {{ parentDir(job.dst) }}</span>
              </template>
            </span>
            <button
              v-if="!isTerminal(job.status)"
              class="action"
              @click="cancel(job)"
              :aria-label="$t('jobs.cancel')"
              :title="$t('jobs.cancel')"
            >
              <i class="material-icons">cancel</i>
            </button>
            <button
              v-else
              class="action"
              @click="jobsStore.dismiss(job.id)"
              :aria-label="$t('jobs.dismiss')"
              :title="$t('jobs.dismiss')"
            >
              <i class="material-icons">close</i>
            </button>
          </div>
          <div v-if="!isTerminal(job.status)" class="file-progress">
            <div :style="{ width: percent(job) + '%' }"></div>
          </div>
          <div class="job-status" :class="job.status">
            {{ statusText(job) }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { partial } from "filesize";
import { useJobsStore } from "@/stores/jobs";
import { useUploadStore } from "@/stores/upload";
import { isTerminal } from "@/api/jobs";
import type { Job } from "@/api/jobs";

const { t } = useI18n({});
const jobsStore = useJobsStore();
const uploadStore = useUploadStore();
const open = ref(true);
const size = partial({ base: 2 });

onMounted(() => {
  jobsStore.init();
});

function actionIcon(job: Job): string {
  switch (job.action) {
    case "copy":
      return "content_copy";
    case "duplicates":
      return "find_in_page";
    default:
      return "drive_file_move";
  }
}

function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : "/";
}

function parentDir(p: string): string {
  const parts = p.split("/").filter(Boolean);
  parts.pop();
  return "/" + parts.join("/");
}

function percent(job: Job): number {
  if (job.totalBytes <= 0) return 0;
  return Math.min(100, (job.doneBytes / job.totalBytes) * 100);
}

function statusText(job: Job): string {
  switch (job.status) {
    case "queued":
      return t("jobs.queued");
    case "running":
      if (job.totalBytes <= 0) return t("jobs.running");
      return t("jobs.progress", {
        done: size(job.doneBytes),
        total: size(job.totalBytes),
        files: job.doneFiles,
        totalFiles: job.totalFiles,
      });
    case "done":
      return t("jobs.done");
    case "canceled":
      return t("jobs.canceled");
    case "failed":
      return t("jobs.failed", { error: job.error || "" });
    default:
      return "";
  }
}

async function cancel(job: Job) {
  if (!confirm(t("jobs.confirmCancel"))) return;
  await jobsStore.cancel(job.id);
}
</script>

<style scoped>
.jobs-panel.stacked .card.floating {
  bottom: 4.5em;
}

.jobs-panel .file-name {
  gap: 0.4em;
}

.jobs-panel .job-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jobs-panel .job-dest {
  opacity: 0.6;
  font-size: 0.85em;
}

.jobs-panel .file-name .action {
  padding: 0;
}

.jobs-panel .job-status {
  font-size: 0.85em;
  opacity: 0.75;
  margin-top: 2px;
}

.jobs-panel .job-status.failed {
  color: var(--red);
  opacity: 1;
}
</style>
