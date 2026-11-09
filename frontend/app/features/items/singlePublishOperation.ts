import { getSinglePublishOperation,publishItem } from './api';

export { getSinglePublishOperation };
export const publishSingleItem = publishItem;
import type { PublishLocation } from './api';
import type { PublishCategory } from './types';
import type { PublishItemForm } from './itemActions';

// 商品发布操作恢复逻辑仅在用户启动普通发布时按需加载，避免挤占商品列表页面分片。
// getOrCreateSinglePublishOperationID 在会话存储中按账号和完整表单内容复用发布操作 ID。
// stablePublishHash 生成仅用于浏览器会话键的稳定短指纹；服务端仍以 SHA-256 重新校验真实请求。
const stablePublishHash = (bytes: Uint8Array): string => {
  // hashA 是第一组 32 位非密码学哈希状态。
  let hashA = 2166136261;
  // hashB 是第二组独立的 32 位非密码学哈希状态。
  let hashB = 5381;
  // byte 是当前输入中的单个字节。
  for (const /* byte 是当前输入中的单个字节。 */ byte of bytes) {
    hashA = Math.imul(hashA ^ byte, 16777619);
    hashB = Math.imul(hashB, 33) ^ byte;
  }
  return (hashA >>> 0).toString(16).padStart(8, '0') + (hashB >>> 0).toString(16).padStart(8, '0');
};

export const getOrCreateSinglePublishOperationID = async (
  form: PublishItemForm,
  location: PublishLocation | null,
  category: PublishCategory | null,
): Promise<string> => {
  // imageEntries 保存商品图片和规格图片及其用途标识。
  const imageEntries = [
    ...(form.images ?? []).map(/* imageFile 表示当前商品主图文件。 */ imageFile => ({ role: 'item', file: imageFile })),
    ...(form.specs ?? []).flatMap(/* spec 表示当前商品规格维度。 */ spec => (spec.values ?? []).flatMap(/* value 表示当前规格维度的规格值。 */ value => value.image ? [{ role: spec.specId + ':' + value.valueId, file: value.image }] : [])),
  ];
  // imageDigests 保存图片用途和浏览器可见元数据；服务端会独立计算图片内容 SHA-256。
  const imageDigests = imageEntries.map(/* entry 表示当前商品图片或规格图片。 */ entry =>
    entry.role + '|' + entry.file.name + '|' + entry.file.type + '|' + entry.file.size + '|' + entry.file.lastModified,
  );
  // canonical 保存不包含图片二进制的规范化表单字段和图片摘要。
  const canonical = JSON.stringify({
    cookie_id: form.cookie_id, title: form.title, description: form.description,
    price: form.price, original_price: form.original_price, quantity: form.quantity,
    postage_mode: form.postage_mode, postage: form.postage,
    specs: (form.specs ?? []).map(/* spec 表示当前参与指纹计算的规格维度。 */ spec => ({
      specId: spec.specId, name: spec.name, supportImage: spec.supportImage,
      values: (spec.values ?? []).map(/* specValue 表示当前规格维度的一个值。 */ specValue => ({ valueId: specValue.valueId, value: specValue.value })),
    })),
    skuRows: form.skuRows ?? [], location, category, imageDigests,
  });
  // digestHex 是当前完整表单的稳定短指纹，仅用于本地查找；服务端会独立计算 SHA-256。
  const digestHex = stablePublishHash(new TextEncoder().encode(canonical));
  // storageKey 按账号保存当前未收口操作，避免用户修改表单后绕过未知结果隔离。
  const storageKey = 'ydisks.single-publish.active.' + encodeURIComponent(form.cookie_id);
  // existingRaw 保存当前账号上一次尚未清理的操作记录。
  let existingRaw: string | null = null;
  try {
    existingRaw = window.sessionStorage.getItem(storageKey);
  } catch {
    throw new Error('浏览器会话存储不可用，无法安全复用商品发布操作 ID；请启用会话存储后重试。');
  }
  if (existingRaw) {
    // existing 保存当前账号上一次操作 ID 及其请求指纹。
    const existing = JSON.parse(existingRaw) as {
      /** operationID 是当前账号尚未清理的发布操作标识。 */ operationID?: string;
      /** fingerprint 是该操作对应的完整请求指纹。 */ fingerprint?: string;
    };
    if (existing.operationID && existing.fingerprint === digestHex) return existing.operationID;
    if (existing.operationID) {
      // previousOperation 保存上一次发布的服务端状态；查询失败时必须停止，不能降级生成新 ID。
      const previousOperation = await getSinglePublishOperation(existing.operationID);
      if (!['prepared', 'local_confirmed'].includes(previousOperation.state)) {
        throw new Error('上一条商品发布操作仍未安全收口。请恢复原表单并查询原操作状态，不要用不同内容开始新发布。');
      }
    }
  }
  // random 保存新操作 ID 的密码学随机字节。
  const random = crypto.getRandomValues(new Uint8Array(16));
  // operationID 是本次发布的稳定随机标识。
  const operationID = Array.from(random).map(/* byte 表示随机操作 ID 中的单个字节。 */ byte => byte.toString(16).padStart(2, '0')).join('');
  try {
    window.sessionStorage.setItem(storageKey, JSON.stringify({ operationID, fingerprint: digestHex }));
  } catch {
    throw new Error('无法持久化商品发布操作 ID；为避免重复创建，已阻止本次发布。');
  }
  return operationID;
};

// clearSinglePublishOperationID 仅在当前操作成功收口后清理会话存储中的活动操作标识。
export const clearSinglePublishOperationID = (cookieID: string, operationID: string): void => {
  // storageKey 是当前账号的活动操作记录键。
  const storageKey = 'ydisks.single-publish.active.' + encodeURIComponent(cookieID);
  try {
    // stored 保存会话存储中的活动操作记录。
    const stored = window.sessionStorage.getItem(storageKey);
    if (stored && (JSON.parse(stored) as { /** operationID 是待清理的当前操作标识。 */ operationID?: string }).operationID === operationID) {
      window.sessionStorage.removeItem(storageKey);
    }
  } catch {
    // 会话存储清理失败不改变服务端已经确认的发布状态。
  }
};
