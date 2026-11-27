import { describe, it, expect, vi } from "vitest";

vi.mock("@/stores/layout", () => ({ useLayoutStore: () => ({}) }));
vi.mock("@/stores/upload", () => ({ useUploadStore: () => ({}) }));
vi.mock("@/api", () => ({ files: {} }));
vi.mock("@/utils/constants", () => ({}));
vi.mock("@/utils/url", () => ({ default: {} }));
vi.mock("@/api/utils", () => ({ removePrefix: (s: string) => s }));

import { applyConflictResolution } from "@/utils/upload";

function entry(name: string, size: number): UploadEntry {
  return { name, size, isDir: false };
}

function conflict(
  index: number,
  serverSize: number,
  localSize: number,
  extra: Partial<ConflictingResource>
): ConflictingResource {
  return {
    index,
    name: `/f${index}`,
    origin: { lastModified: 0, size: localSize },
    dest: { lastModified: 0, size: serverSize },
    checked: ["origin"],
    isSmallerOnServer: localSize > serverSize,
    ...extra,
  };
}

describe("applyConflictResolution", () => {
  it("resumes interrupted uploads from the server's size", () => {
    const files = [entry("big.iso", 1000), entry("done.iso", 500)];
    applyConflictResolution(files, [
      conflict(0, 400, 1000, { checked: ["origin"], resume: true }),
      conflict(1, 500, 500, { checked: ["dest"] }),
    ]);

    expect(files).toHaveLength(1);
    expect(files[0].name).toBe("big.iso");
    expect(files[0].overwrite).toBe(true);
    expect(files[0].resumeFrom).toBe(400);
  });

  it("replaces without resuming when resume was not chosen", () => {
    const files = [entry("a.bin", 1000)];
    applyConflictResolution(files, [conflict(0, 400, 1000, {})]);

    expect(files[0].overwrite).toBe(true);
    expect(files[0].resumeFrom).toBeUndefined();
  });

  it("never resumes a file that isn't smaller on the server", () => {
    const files = [entry("a.bin", 1000)];
    applyConflictResolution(files, [conflict(0, 1000, 1000, { resume: true })]);

    expect(files[0].resumeFrom).toBeUndefined();
  });
});
