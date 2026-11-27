type ApiMethod = "GET" | "POST" | "PUT" | "DELETE" | "PATCH";

type ApiContent =
  Blob | File | Pick<ReadableStreamDefaultReader<any>, "read"> | "";

interface ApiOpts {
  method?: ApiMethod;
  headers?: object;
  body?: any;
  signal?: AbortSignal;
}

interface TusSettings {
  retryCount: number;
  chunkSize: number;
}

type ChecksumAlg = "md5" | "sha1" | "sha256" | "sha512";

interface Share {
  hash: string;
  path: string;
  expire?: any;
  userID?: number;
  hasPassword?: boolean;
  username?: string;
  note?: string;
  created?: number;
  maxDownloads?: number;
  downloads?: number;
}

interface ShareOptions {
  password?: string;
  expires?: number;
  unit?: string;
  note?: string;
  maxDownloads?: number;
}

/** What a public share page is told about the link it was opened through. */
interface PublicShareInfo {
  note?: string;
  expire?: number;
  downloadsLeft?: number;
}

interface SearchParams {
  [key: string]: string;
}
