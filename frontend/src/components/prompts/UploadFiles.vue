<template>
  <div
    v-if="uploadStore.activeUploads.size > 0 || failed.length > 0"
    class="upload-files"
    v-bind:class="{ closed: !open }"
  >
    <div class="card floating">
      <div class="card-title">
        <h2 v-if="uploadStore.activeUploads.size > 0">
          {{
            $t("prompts.uploadFiles", {
              files: uploadStore.pendingUploadCount,
            })
          }}
        </h2>
        <h2 v-else>
          {{ $t("uploadStatus.failedTitle", { count: failed.length }) }}
        </h2>
        <div v-if="uploadStore.activeUploads.size > 0" class="upload-info">
          <div class="upload-speed">{{ speedText }}/s</div>
          <div class="upload-eta">{{ formattedETA }} remaining</div>
          <div class="upload-percentage">{{ sentPercent }}% Completed</div>
          <div class="upload-fraction">
            {{ sentMbytes }} /
            {{ totalMbytes }}
          </div>
        </div>
        <button
          v-if="uploadStore.activeUploads.size > 0"
          class="action"
          @click="abortAll"
          aria-label="Abort upload"
          title="Abort upload"
        >
          <i class="material-icons">{{ "cancel" }}</i>
        </button>
        <button
          class="action"
          @click="toggle"
          aria-label="Toggle file upload list"
          title="Toggle file upload list"
        >
          <i class="material-icons">{{
            open ? "keyboard_arrow_down" : "keyboard_arrow_up"
          }}</i>
        </button>
      </div>

      <div class="card-content file-icons">
        <div
          class="file"
          v-for="upload in uploadStore.activeUploads"
          :key="upload.path"
          :data-dir="upload.type === 'dir'"
          :data-type="upload.type"
          :aria-label="upload.name"
        >
          <div class="file-name">
            <i class="material-icons"></i> {{ upload.name }}
          </div>
          <div class="file-progress">
            <div
              v-bind:style="{
                width: (upload.sentBytes / upload.totalBytes) * 100 + '%',
              }"
            ></div>
          </div>
          <div
            v-if="upload.status"
            class="file-status"
            :class="upload.status.kind"
          >
            {{ statusText(upload.status) }}
          </div>
        </div>

        <div v-if="failed.length > 0" class="failed-uploads">
          <div class="failed-title">
            <span>{{
              $t("uploadStatus.failedTitle", { count: failed.length })
            }}</span>
            <button
              class="button button--flat"
              @click="uploadStore.clearFailed()"
            >
              {{ $t("uploadStatus.clearFailed") }}
            </button>
          </div>
          <div
            class="file failed"
            v-for="f in failed"
            :key="f.path"
            :title="f.path"
          >
            <div class="file-name">
              <i class="material-icons">error_outline</i> {{ f.name }}
            </div>
            <div class="file-status failed">{{ f.message }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useFileStore } from "@/stores/file";
import { useUploadStore } from "@/stores/upload";
import { storeToRefs } from "pinia";
import { computed, ref, watch } from "vue";
import buttons from "@/utils/buttons";
import { useI18n } from "vue-i18n";
import { partial } from "filesize";

const { t } = useI18n({});

const open = ref<boolean>(false);
const speed = ref<number>(0);
const eta = ref<number>(Infinity);

const fileStore = useFileStore();
const uploadStore = useUploadStore();

const {
  sentBytes,
  totalBytes,
  failedUploads: failed,
} = storeToRefs(uploadStore);
const sizeText = partial({ base: 2 });

const statusText = (status: UploadStatus): string => {
  switch (status.kind) {
    case "resuming":
      return t("uploadStatus.resuming", { size: sizeText(status.from) });
    case "resumed":
      return t("uploadStatus.resumed", { size: sizeText(status.from) });
    case "restarted":
      return t("uploadStatus.restarted");
    case "retrying":
      return t("uploadStatus.retrying", {
        attempt: status.attempt,
        of: status.of,
      });
    case "failed":
      return t("uploadStatus.failed", { message: status.message });
  }
};

const byteToMbyte = partial({ exponent: 2 });
const byteToKbyte = partial({ exponent: 1 });

const sentPercent = computed(() =>
  ((uploadStore.sentBytes / uploadStore.totalBytes) * 100).toFixed(2)
);

const sentMbytes = computed(() => byteToMbyte(uploadStore.sentBytes));
const totalMbytes = computed(() => byteToMbyte(uploadStore.totalBytes));
const speedText = computed(() => {
  const bytes = speed.value;

  if (bytes < 1024 * 1024) {
    const kb = parseFloat(byteToKbyte(bytes));
    return `${kb.toFixed(2)} KB`;
  } else {
    const mb = parseFloat(byteToMbyte(bytes));
    return `${mb.toFixed(2)} MB`;
  }
});

let lastSpeedUpdate: number = 0;
let recentSpeeds: number[] = [];

let lastThrottleTime = 0;

const throttledCalculateSpeed = (sentBytes: number, oldSentBytes: number) => {
  const now = Date.now();
  if (now - lastThrottleTime < 100) {
    return;
  }

  lastThrottleTime = now;
  calculateSpeed(sentBytes, oldSentBytes);
};

const calculateSpeed = (sentBytes: number, oldSentBytes: number) => {
  // Reset the state when the uploads batch is complete
  if (sentBytes === 0) {
    lastSpeedUpdate = 0;
    recentSpeeds = [];

    eta.value = Infinity;
    speed.value = 0;

    return;
  }

  const elapsedTime = (Date.now() - (lastSpeedUpdate ?? 0)) / 1000;
  const bytesSinceLastUpdate = sentBytes - oldSentBytes;
  const currentSpeed = bytesSinceLastUpdate / elapsedTime;

  recentSpeeds.push(currentSpeed);
  if (recentSpeeds.length > 5) {
    recentSpeeds.shift();
  }

  const recentSpeedsAverage =
    recentSpeeds.reduce((acc, curr) => acc + curr) / recentSpeeds.length;

  // Use the current speed for the first update to avoid smoothing lag
  if (recentSpeeds.length === 1) {
    speed.value = currentSpeed;
  }

  speed.value = recentSpeedsAverage * 0.2 + speed.value * 0.8;

  lastSpeedUpdate = Date.now();

  calculateEta();
};

const calculateEta = () => {
  if (speed.value === 0) {
    eta.value = Infinity;

    return Infinity;
  }

  const remainingSize = uploadStore.totalBytes - uploadStore.sentBytes;
  const speedBytesPerSecond = speed.value;

  eta.value = remainingSize / speedBytesPerSecond;
};

watch(sentBytes, throttledCalculateSpeed);

watch(totalBytes, (totalBytes, oldTotalBytes) => {
  if (oldTotalBytes !== 0) {
    return;
  }

  // Mark the start time of a new upload batch
  lastSpeedUpdate = Date.now();
});

const formattedETA = computed(() => {
  if (!eta.value || eta.value === Infinity) {
    return "--:--:--";
  }

  let totalSeconds = eta.value;
  const hours = Math.floor(totalSeconds / 3600);
  totalSeconds %= 3600;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = Math.round(totalSeconds % 60);

  return `${hours.toString().padStart(2, "0")}:${minutes
    .toString()
    .padStart(2, "0")}:${seconds.toString().padStart(2, "0")}`;
});

const toggle = () => {
  open.value = !open.value;
};

const abortAll = () => {
  if (confirm(t("upload.abortUpload"))) {
    buttons.done("upload");
    open.value = false;
    uploadStore.abort();
    fileStore.reload = true; // Trigger reload in the file store
  }
};
</script>

<style scoped>
.upload-info {
  min-width: 19ch;
  width: auto;
  text-align: left;
}
</style>
