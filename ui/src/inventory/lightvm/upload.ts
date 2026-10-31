import { api } from "../../shared/api";

export type UploadedLightVMFile = {
  path: string;
  file_name: string;
  size_bytes: number;
};

type UploadKind = "kernel" | "initramfs";

function parseError(xhr: XMLHttpRequest): string {
  const body = xhr.response;
  if (body && typeof body === "object" && "error" in body && typeof body.error === "string") {
    return body.error;
  }
  return xhr.statusText || `Upload failed (${xhr.status})`;
}

// Uploads stream through the native multipart endpoint. Retrying an upload
// automatically could leave duplicate files, so failures are reported to the
// caller instead of using the shared retry loop.
export function uploadLightVMFile(
  kind: UploadKind,
  file: File,
  onProgress?: (fraction: number) => void
): Promise<UploadedLightVMFile> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("kind", kind);
    form.append("file", file, file.name);

    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/lightvm/uploads");
    xhr.responseType = "json";
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) {
        onProgress?.(event.loaded / event.total);
      }
    };
    xhr.onerror = () => reject(new Error("Upload failed because the API could not be reached"));
    xhr.onabort = () => reject(new Error("Upload was cancelled"));
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300 && xhr.response) {
        resolve(xhr.response as UploadedLightVMFile);
        return;
      }
      reject(new Error(parseError(xhr)));
    };
    xhr.send(form);
  });
}

export async function removeUploadedLightVMFile(path: string): Promise<void> {
  if (!path) {
    return;
  }
  await api(`/api/lightvm/uploads?path=${encodeURIComponent(path)}`, { method: "DELETE" });
}
