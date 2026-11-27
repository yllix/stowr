type UploadStatus =
  | { kind: "resuming"; from: number }
  | { kind: "resumed"; from: number }
  | { kind: "restarted" }
  | { kind: "retrying"; attempt: number; of: number }
  | { kind: "failed"; message: string };

type Upload = {
  path: string;
  name: string;
  file: File | null;
  type: ResourceType;
  overwrite: boolean;
  // Bytes of this file already on the server from an interrupted upload.
  resumeFrom?: number;
  status?: UploadStatus;
  totalBytes: number;
  sentBytes: number;
  rawProgress: {
    sentBytes: number;
  };
};

interface UploadEntry {
  name: string;
  size: number;
  isDir: boolean;
  fullPath?: string;
  to?: string;
  file?: File;
  overwrite?: boolean;
  resumeFrom?: number;
}

type UploadList = UploadEntry[];
