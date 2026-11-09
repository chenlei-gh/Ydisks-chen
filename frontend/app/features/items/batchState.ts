import type { PublishBatchDetailRow } from './types';

// PublishBatchSummary 是选择最近批量任务时所需的最小状态模型。
export interface PublishBatchSummary {
  // id 是批量任务标识。
  id?: string;
  // status 是批量任务状态。
  status?: string;
}

// selectActivePublishBatch 只选择仍可继续处理或等待 worker 接管的活跃任务。
export const selectActivePublishBatch = <T extends PublishBatchSummary>(batches: T[]): T | undefined =>
  batches.find(
    // 活跃任务筛选器排除已完成历史，避免覆盖新的上传流程。
    batch => batch.status === 'pending' || batch.status === 'running' || batch.status === 'canceling',
  );

// isBatchInProgress 判断批量任务是否仍需要继续轮询。
export const isBatchInProgress = (status?: string): boolean => status === 'pending' || status === 'running' || status === 'canceling';

// BatchPreviewGate 是判断预检是否可以启动任务的最小模型。
export interface BatchPreviewGate {
  // preview_id 是预检任务标识。
  preview_id?: string;
  // valid 是可发布行数。
  valid?: number;
}

// canStartBatch 判断预检结果是否包含可发布商品。
export const canStartBatch = (preview?: BatchPreviewGate | null): boolean =>
  Boolean(preview?.preview_id && Number(preview.valid || 0) > 0);

// BatchRetryGate 是判断批量任务是否可重试的最小模型。
export interface BatchRetryGate {
  // id 是批量任务标识。
  id?: string;
  // retryable 是可重试失败行数。
  retryable?: number;
  // status 是批量任务状态。
  status?: string;
}

// canRetryBatch 判断批量任务是否存在可重试失败行。
export const canRetryBatch = (detail?: BatchRetryGate | null): boolean =>
  Boolean(detail?.id && Number(detail.retryable || 0) > 0 && !isBatchInProgress(detail.status));

// isCurrentBatchRequest 判断轮询响应是否仍属于当前任务请求代次。
export const isCurrentBatchRequest = (requestGeneration: number, currentGeneration: number): boolean =>
  requestGeneration === currentGeneration;

// batchStatusText 将后端批量状态转换为中文展示文本。
export const batchStatusText = (status?: string): string => {
  switch (status) {
    case 'preview': return '待确认';
    case 'pending': return '等待中';
    case 'running': return '发布中';
    case 'canceling': return '正在安全取消';
    case 'success': return '成功';
    case 'failed': return '失败';
    case 'completed': return '已完成';
    case 'partially_failed': return '部分失败';
    case 'canceled': return '已取消';
    default: return status || '-';
  }
};

// batchStatusClass 返回批量状态对应的标签样式。
export const batchStatusClass = (status?: string): string => {
  switch (status) {
    case 'success':
    case 'completed':
      return 'bg-emerald-50 text-emerald-700 border-emerald-100';
    case 'partially_failed':
    case 'failed':
      return 'bg-red-50 text-red-700 border-red-100';
    case 'running':
      return 'bg-blue-50 text-blue-700 border-blue-100';
    case 'canceled':
      return 'bg-gray-100 text-gray-600 border-gray-200';
    default:
      return 'bg-amber-50 text-amber-700 border-amber-100';
  }
};


// csvCell 转义 CSV 字段并阻止 Excel 将不可信文本识别为公式。
const csvCell = (value: unknown): string => {
  // raw 是待导出的原始单元格文本。
  const raw = String(value ?? '');
  // safe 在可能触发电子表格公式执行的前缀前添加文本标记。
  const safe = /^[\t\r\n ]*[=+\-@]/.test(raw) ? `'${raw}` : raw;
  return `"${safe.replace(/"/g, '""')}"`;
};

// buildBatchResultsCSV 将当前批次逐行结果序列化为可供表格软件打开的 UTF-8 CSV。
export const buildBatchResultsCSV = (rows: PublishBatchDetailRow[]): string => {
  // headers 保存导出文件的稳定列顺序。
  const headers = ['原始行号', '账号 ID', '标题', '价格', '库存', '状态', '类目', '商品 ID', '商品链接', '失败类型', '错误原因'];
  // records 保存标题行和当前服务端返回的逐行结果。
  const records = [
    headers,
    ...rows.map(row => [
      row.row_no,
      row.cookie_id,
      row.title,
      row.price,
      row.quantity,
      batchStatusText(row.status),
      row.category?.cat_name || '',
      row.item_id,
      row.item_url,
      row.failure_kind,
      row.error_message,
    ]),
  ];
  return `\uFEFF${records.map(record => record.map(csvCell).join(',')).join('\r\n')}`;
};
