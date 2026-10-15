import { request } from "./api-client";

// AF-16 bulk import/export. Each kind belongs to the service that owns the
// data: products and prices to Catalog, stock receipts to Inventory. The
// server validates every row again and applies it through the same rules
// as single edits; this module only shapes requests and display.

export type BulkKind = "products" | "prices" | "stock_receipts";
export type BulkOwner = "catalog" | "inventory";

export const BULK_KINDS: {
  kind: BulkKind;
  owner: BulkOwner;
  label: string;
  read: string;
  write: string;
}[] = [
  {
    kind: "prices",
    owner: "catalog",
    label: "Giá bán",
    read: "products.read",
    write: "products.write",
  },
  {
    kind: "products",
    owner: "catalog",
    label: "Sản phẩm mới (nháp)",
    read: "products.read",
    write: "products.write",
  },
  {
    kind: "stock_receipts",
    owner: "inventory",
    label: "Nhập kho",
    read: "inventory.read",
    write: "inventory.adjust",
  },
];

export type TemplateColumn = {
  name: string;
  required: boolean;
  read_only?: boolean;
  description: string;
  example?: string;
};
export type BulkTemplate = {
  kind: BulkKind;
  version: number;
  columns: TemplateColumn[];
  dynamic_prefix?: string;
  notes: string[];
  exportable: boolean;
  enabled: boolean;
  disabled_reason?: string;
  max_rows: number;
  max_bytes: number;
};
export type BulkSummary = {
  total: number;
  invalid: number;
  unchanged: number;
  pending: number;
  succeeded: number;
  needs_approval: number;
  failed: number;
  cancelled: number;
};
export type JobStatus =
  | "uploaded"
  | "validating"
  | "preview_ready"
  | "file_invalid"
  | "failed"
  | "confirmed"
  | "applying"
  | "cancelling"
  | "cancelled"
  | "completed";
export type BulkJob = {
  id: string;
  vendor_id: string;
  kind: BulkKind;
  template_version: number;
  status: JobStatus;
  outcome?: "succeeded" | "partial" | "failed";
  file_name: string;
  file_bytes: number;
  header: string[];
  preview_hash?: string;
  preview_at?: string;
  duplicate_of?: string;
  error_code?: string;
  error_message?: string;
  version: number;
  summary: BulkSummary;
  created_at: string;
  finished_at?: string;
};
export type RowStatus =
  | "invalid"
  | "unchanged"
  | "valid"
  | "applying"
  | "succeeded"
  | "needs_approval"
  | "failed"
  | "cancelled";
export type BulkRow = {
  row_no: number;
  status: RowStatus;
  target_key?: string;
  values: string[];
  old_values?: Record<string, unknown>;
  new_values?: Record<string, unknown>;
  result_ref?: string;
  error?: { code: string; field?: string; message: string };
};
export type BulkExport = {
  id: string;
  kind: BulkKind;
  status: "pending" | "processing" | "ready" | "failed" | "expired";
  row_count: number;
  expires_at?: string;
};
export type Page<T> = { items: T[]; next_cursor?: string };
export type Download = { url: string; expires_at: string };

export const ownerOf = (kind: BulkKind): BulkOwner =>
  BULK_KINDS.find((k) => k.kind === kind)!.owner;
const base = (owner: BulkOwner) => `/api/${owner}/vendor`;
const id = encodeURIComponent;

export const getTemplate = (token: string, kind: BulkKind, vendor: string) =>
  request<BulkTemplate>(`${base(ownerOf(kind))}/import-templates/${kind}`, {
    token,
    query: { vendor_id: vendor },
  });
export const listJobs = (token: string, kind: BulkKind, vendor: string, cursor = "") =>
  request<Page<BulkJob>>(`${base(ownerOf(kind))}/import-jobs`, {
    token,
    query: { vendor_id: vendor, kind, cursor, limit: 10 },
  });
export function uploadJob(
  token: string,
  key: string,
  vendor: string,
  kind: BulkKind,
  version: number,
  file: File,
) {
  const form = new FormData();
  form.set("vendor_id", vendor);
  form.set("kind", kind);
  form.set("template_version", String(version));
  form.set("file", file);
  return request<BulkJob>(`${base(ownerOf(kind))}/import-jobs`, {
    token,
    method: "POST",
    form,
    headers: { "Idempotency-Key": key },
  });
}
export const getJob = (token: string, owner: BulkOwner, job: string) =>
  request<BulkJob>(`${base(owner)}/import-jobs/${id(job)}`, { token });
export const listRows = (
  token: string,
  owner: BulkOwner,
  job: string,
  status: string,
  cursor = "",
) =>
  request<Page<BulkRow>>(`${base(owner)}/import-jobs/${id(job)}/rows`, {
    token,
    query: { status, cursor, limit: 50 },
  });
export const confirmJob = (
  token: string,
  owner: BulkOwner,
  job: BulkJob,
  acknowledgeDuplicate: boolean,
) =>
  request<BulkJob>(`${base(owner)}/import-jobs/${id(job.id)}/confirm`, {
    token,
    method: "POST",
    json: {
      preview_hash: job.preview_hash,
      expected_version: job.version,
      acknowledge_duplicate_file: acknowledgeDuplicate,
    },
  });
export const cancelJob = (token: string, owner: BulkOwner, job: string) =>
  request<BulkJob>(`${base(owner)}/import-jobs/${id(job)}/cancellations`, {
    token,
    method: "POST",
    json: {},
  });
export const reportLink = (token: string, owner: BulkOwner, job: string) =>
  request<Download>(`${base(owner)}/import-jobs/${id(job)}/report-downloads`, {
    token,
    method: "POST",
  });
export const createExport = (token: string, key: string, vendor: string, kind: BulkKind) =>
  request<BulkExport>(`${base(ownerOf(kind))}/export-jobs`, {
    token,
    method: "POST",
    json: { vendor_id: vendor, kind },
    headers: { "Idempotency-Key": key },
  });
export const getExport = (token: string, owner: BulkOwner, exportId: string) =>
  request<BulkExport>(`${base(owner)}/export-jobs/${id(exportId)}`, { token });
export const exportLink = (token: string, owner: BulkOwner, exportId: string) =>
  request<Download>(`${base(owner)}/export-jobs/${id(exportId)}/download-links`, {
    token,
    method: "POST",
  });

// Operators (AF-16 rollback drain): list and stop jobs of any shop.
export const adminJobs = (token: string, owner: BulkOwner, status = "", cursor = "") =>
  request<Page<BulkJob>>(`/api/${owner}/admin/import-jobs`, {
    token,
    query: { status, cursor, limit: 20 },
  });
export const adminCancel = (token: string, owner: BulkOwner, job: string, reason: string) =>
  request<BulkJob>(`/api/${owner}/admin/import-jobs/${id(job)}/cancellations`, {
    token,
    method: "POST",
    json: { reason },
  });

// A cell a spreadsheet would run as a formula is written as text.
export function csvCell(value: string): string {
  const safe = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return /[",\r\n]/.test(safe) ? `"${safe.replace(/"/g, '""')}"` : safe;
}

// templateCsv is the empty template (with a BOM, so spreadsheets read it
// as UTF-8): the importable columns and one example row to overwrite.
export function templateCsv(t: BulkTemplate): string {
  const columns = t.columns.filter((c) => !c.read_only);
  const lines = [
    columns.map((c) => csvCell(c.name)).join(","),
    columns.map((c) => csvCell(c.example ?? "")).join(","),
  ];
  return "﻿" + lines.join("\r\n") + "\r\n";
}

// precheck refuses files the server would refuse anyway, before upload.
export function precheck(file: File, t: BulkTemplate): string | null {
  if (file.size === 0) return "Tệp trống.";
  if (file.size > t.max_bytes)
    return `Tệp lớn hơn ${Math.floor(t.max_bytes / 1024 / 1024)} MB; hãy tách thành nhiều tệp.`;
  if (!/\.csv$/i.test(file.name)) return "Chỉ nhận tệp .csv (UTF-8).";
  return null;
}

export const ACTIVE_STATUSES: JobStatus[] = [
  "uploaded",
  "validating",
  "confirmed",
  "applying",
  "cancelling",
];
export const isActive = (job?: BulkJob) => !!job && ACTIVE_STATUSES.includes(job.status);
export const canCancel = (job: BulkJob) =>
  ["uploaded", "validating", "preview_ready", "confirmed", "applying"].includes(job.status);
export const hasProblems = (s: BulkSummary) => s.invalid + s.failed + s.cancelled > 0;

export const JOB_STATUS_LABELS: Record<JobStatus, string> = {
  uploaded: "Đã tải lên, chờ kiểm tra",
  validating: "Đang kiểm tra",
  preview_ready: "Đã kiểm tra, chờ xác nhận",
  file_invalid: "Tệp không hợp lệ",
  failed: "Không kiểm tra được",
  confirmed: "Đã xác nhận, chờ áp dụng",
  applying: "Đang áp dụng",
  cancelling: "Đang dừng",
  cancelled: "Đã hủy",
  completed: "Hoàn tất",
};
export const OUTCOME_LABELS = {
  succeeded: "Thành công toàn bộ",
  partial: "Thành công một phần",
  failed: "Không dòng nào được áp dụng",
};
export const ROW_STATUS_LABELS: Record<RowStatus, string> = {
  invalid: "Lỗi",
  unchanged: "Không đổi",
  valid: "Hợp lệ, chờ áp dụng",
  applying: "Đang áp dụng",
  succeeded: "Đã áp dụng",
  needs_approval: "Chờ sàn duyệt",
  failed: "Thất bại",
  cancelled: "Đã dừng",
};

const ERROR_MESSAGES: Record<string, string> = {
  file_invalid: "Tệp không đúng định dạng mẫu",
  preview_stale: "Dữ liệu đã thay đổi sau khi kiểm tra; hãy tải lại trang hoặc tạo lượt nhập mới.",
  duplicate_file: "Tệp này đã được áp dụng ở lượt nhập trước.",
  bulk_operations_disabled: "Tính năng nhập hàng loạt chưa được bật cho mẫu này",
  nothing_to_apply: "Không có dòng hợp lệ để áp dụng.",
  template_version_mismatch: "Mẫu đã đổi phiên bản; hãy tải mẫu mới.",
  idempotency_conflict: "Lượt tải lên trước dùng tệp khác; hãy chọn lại tệp.",
};

// errorText keeps the server's detail (it names the line or column) and
// adds a Vietnamese summary for known codes.
export function errorText(code: string, message: string): string {
  const known = ERROR_MESSAGES[code];
  if (!known) return message;
  return message && message !== known ? `${known}: ${message}` : known;
}

const VALUE_LABELS: Record<string, string> = {
  price_amount: "Giá",
  available: "Khả dụng",
  reserved: "Đang giữ",
  status: "Trạng thái",
  name: "Tên",
  product_name: "Sản phẩm",
  sku: "SKU",
  category_id: "Danh mục",
  product_id: "Sản phẩm",
  requires_review: "Cần duyệt lại",
  needs_approval: "Cần sàn duyệt",
};

// describeValues renders old/new values of a row as short text.
export function describeValues(values?: Record<string, unknown>): string {
  if (!values) return "";
  return Object.entries(values)
    .filter(([, v]) => v !== "" && v !== null && v !== undefined)
    .map(([k, v]) => `${VALUE_LABELS[k] ?? k}: ${v === true ? "có" : String(v)}`)
    .join(" · ");
}
