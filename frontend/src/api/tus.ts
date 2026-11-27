import * as tus from "tus-js-client";
import { baseURL, tusEndpoint, tusSettings, origin } from "@/utils/constants";
import { useAuthStore } from "@/stores/auth";
import { removePrefix } from "@/api/utils";

const RETRY_BASE_DELAY = 1000;
const RETRY_MAX_DELAY = 20000;
const CURRENT_UPLOAD_LIST: { [key: string]: tus.Upload } = {};

// How much of the already-uploaded data is sent back to prove that a partial
// file on the server is the start of this file. Must not exceed the server's
// limit (resumeSampleMax).
const RESUME_SAMPLE_BYTES = 1024 * 1024;

export interface UploadOptions {
  // Bytes already on the server from an interrupted upload of this file.
  resumeFrom?: number;
  // Reports what the upload is doing, for the upload panel.
  onStatus?: (status: UploadStatus | undefined) => void;
}

/**
 * Ask the server to continue an interrupted upload. Resolves with the upload
 * URL to continue at, or null when the partial file on the server isn't the
 * start of this file (it must then be uploaded from the beginning).
 */
async function resumeUpload(
  resourceURL: string,
  content: Blob,
  resumeFrom: number
): Promise<string | null> {
  const authStore = useAuthStore();
  const start = Math.max(0, resumeFrom - RESUME_SAMPLE_BYTES);
  const sample = content.slice(start, resumeFrom);

  const res = await fetch(`${resourceURL}&resume=true`, {
    method: "POST",
    headers: {
      "X-Auth": authStore.jwt,
      "Tus-Resumable": "1.0.0",
      "Upload-Length": String(content.size),
      "Stowr-Resume-Offset": String(resumeFrom),
    },
    body: sample,
  });

  if (res.status === 409) return null;
  if (res.status !== 201) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status} ${text || res.statusText}`.trim());
  }

  const location = res.headers.get("Location");
  if (!location) throw new Error("Resume failed: no upload location");
  return `${origin}${location}`;
}

export async function upload(
  filePath: string,
  content: ApiContent = "",
  overwrite = false,
  onupload: any,
  options: UploadOptions = {}
) {
  if (!tusSettings) {
    // Shouldn't happen as we check for tus support before calling this function
    throw new Error("Tus.io settings are not defined");
  }

  filePath = removePrefix(filePath);
  const authStore = useAuthStore();

  // Exit early because of typescript, tus content can't be a string
  if (content === "") {
    return false;
  }

  const onStatus = options.onStatus ?? (() => {});
  let uploadUrl: string | null = null;
  let baseStatus: UploadStatus | undefined;

  const resumeFrom = options.resumeFrom ?? 0;
  if (resumeFrom > 0 && content instanceof Blob && resumeFrom < content.size) {
    onStatus({ kind: "resuming", from: resumeFrom });
    uploadUrl = await resumeUpload(
      `${origin}${baseURL}${tusEndpoint}${filePath}?override=false`,
      content,
      resumeFrom
    );
    if (uploadUrl) {
      baseStatus = { kind: "resumed", from: resumeFrom };
    } else {
      // The server copy is a different file: replace it.
      baseStatus = { kind: "restarted" };
      overwrite = true;
    }
    onStatus(baseStatus);
  }

  const resourcePath = `${tusEndpoint}${filePath}?override=${overwrite}`;
  const retryDelays = computeRetryDelays(tusSettings);
  let retrying = false;

  return new Promise<void | string>((resolve, reject) => {
    const upload = new tus.Upload(content, {
      endpoint: `${origin}${baseURL}${resourcePath}`,
      uploadUrl: uploadUrl ?? undefined,
      chunkSize: tusSettings!.chunkSize,
      retryDelays,
      parallelUploads: 1,
      storeFingerprintForResuming: false,
      headers: {
        "X-Auth": authStore.jwt,
      },
      onShouldRetry: function (err, retryAttempt) {
        const status = err.originalResponse
          ? err.originalResponse.getStatus()
          : 0;

        // Do not retry for file conflict.
        if (status === 409) {
          return false;
        }

        retrying = true;
        onStatus({
          kind: "retrying",
          attempt: retryAttempt + 1,
          of: retryDelays?.length ?? 0,
        });
        return true;
      },
      onError: function (error: Error | tus.DetailedError) {
        delete CURRENT_UPLOAD_LIST[filePath];

        if (error.message === "Upload aborted") {
          return reject(error);
        }

        const message =
          error instanceof tus.DetailedError
            ? error.originalResponse === null
              ? "000 No connection"
              : error.originalResponse.getBody()
            : "Upload failed";

        console.error(error);
        onStatus({ kind: "failed", message });

        reject(new Error(message));
      },
      onProgress: function (bytesUploaded) {
        if (retrying) {
          retrying = false;
          onStatus(baseStatus);
        }
        if (typeof onupload === "function") {
          onupload({ loaded: bytesUploaded });
        }
      },
      onSuccess: function () {
        delete CURRENT_UPLOAD_LIST[filePath];
        resolve();
      },
    });
    CURRENT_UPLOAD_LIST[filePath] = upload;
    upload.start();
  });
}

function computeRetryDelays(tusSettings: TusSettings): number[] | undefined {
  if (!tusSettings.retryCount || tusSettings.retryCount < 1) {
    // Disable retries altogether
    return undefined;
  }
  // The tus client expects our retries as an array with computed backoffs
  // E.g.: [0, 3000, 5000, 10000, 20000]
  const retryDelays = [];
  let delay = 0;

  for (let i = 0; i < tusSettings.retryCount; i++) {
    retryDelays.push(Math.min(delay, RETRY_MAX_DELAY));
    delay =
      delay === 0 ? RETRY_BASE_DELAY : Math.min(delay * 2, RETRY_MAX_DELAY);
  }

  return retryDelays;
}

export async function useTus(content: ApiContent) {
  return isTusSupported() && content instanceof Blob;
}

function isTusSupported() {
  return tus.isSupported === true;
}

export function abortAllUploads() {
  for (const filePath in CURRENT_UPLOAD_LIST) {
    if (CURRENT_UPLOAD_LIST[filePath]) {
      CURRENT_UPLOAD_LIST[filePath].abort(true);
      CURRENT_UPLOAD_LIST[filePath].options!.onError!(
        new Error("Upload aborted")
      );
    }
    delete CURRENT_UPLOAD_LIST[filePath];
  }
}
