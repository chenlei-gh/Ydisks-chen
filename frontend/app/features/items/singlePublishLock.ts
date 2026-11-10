import type { PublishLocation } from './api';
import type { PublishCategory } from './types';
import type { PublishItemForm } from './itemActions';

// PublishLockManager 只描述本功能需要的 Web Locks 最小接口，便于测试并避免引入额外依赖。
export interface PublishLockManager {
  request<T>(
    name: string,
    options: { mode: 'exclusive'; ifAvailable?: boolean },
    callback: (lock: Lock | null) => Promise<T>,
  ): Promise<T>;
}

// getSinglePublishTabLockName 为相同账号和商品内容生成跨标签页一致的锁名，不写入持久存储。
export const getSinglePublishTabLockName = (
  form: PublishItemForm,
  location: PublishLocation | null,
  category: PublishCategory | null,
): string => {
  // imageEntries 保存主图和规格图片的用途及浏览器可见元数据。
  const imageEntries = [
    ...(form.images ?? []).map(image => ({ role: 'item', file: image })),
    ...(form.specs ?? []).flatMap(spec => (spec.values ?? []).flatMap(value =>
      value.image ? [{ role: spec.specId + ':' + value.valueId, file: value.image }] : [],
    )),
  ];
  // canonical 是参与锁指纹的商品发布字段，不包含图片二进制和账号 Cookie 内容。
  const canonical = JSON.stringify({
    cookie_id: form.cookie_id,
    title: form.title,
    description: form.description,
    price: form.price,
    original_price: form.original_price,
    quantity: form.quantity,
    postage_mode: form.postage_mode,
    postage: form.postage,
    specs: (form.specs ?? []).map(spec => ({
      specId: spec.specId,
      name: spec.name,
      supportImage: spec.supportImage,
      values: (spec.values ?? []).map(value => ({ valueId: value.valueId, value: value.value })),
    })),
    skuRows: form.skuRows ?? [],
    location,
    category,
    imageEntries: imageEntries.map(entry => ({
      role: entry.role,
      name: entry.file.name,
      type: entry.file.type,
      size: entry.file.size,
      lastModified: entry.file.lastModified,
    })),
  });
  // hashA 和 hashB 是用于浏览器锁名的两组非密码学短哈希状态。
  let hashA = 2166136261;
  let hashB = 5381;
  for (const byte of new TextEncoder().encode(canonical)) {
    hashA = Math.imul(hashA ^ byte, 16777619);
    hashB = Math.imul(hashB, 33) ^ byte;
  }
  return 'ydisks:single-publish:' + encodeURIComponent(form.cookie_id) + ':' +
    (hashA >>> 0).toString(16).padStart(8, '0') + (hashB >>> 0).toString(16).padStart(8, '0');
};

// runSinglePublishWithTabLock 在同浏览器标签页间协调相同商品发布；不支持 Web Locks 时保持原发布流程。
export const runSinglePublishWithTabLock = async <T>(
  lockName: string,
  task: () => Promise<T>,
  confirmConflict: () => boolean,
  lockManager?: PublishLockManager,
): Promise<{ status: 'completed'; value: T } | { status: 'cancelled' }> => {
  // manager 是当前浏览器提供的 Web Locks 实现；服务端仍负责操作 ID 幂等。
  const manager = lockManager ?? (typeof navigator !== 'undefined' && 'locks' in navigator
    ? navigator.locks
    : undefined);
  if (!manager) return { status: 'completed', value: await task() };

  // acquired 标记首次非等待探测是否取得锁。
  let acquired = false;
  // result 保存在首次取得锁时执行任务的结果。
  let result!: T;
  await manager.request(lockName, { mode: 'exclusive', ifAvailable: true }, async lock => {
    if (!lock) return;
    acquired = true;
    result = await task();
  });
  if (acquired) return { status: 'completed', value: result };
  if (!confirmConflict()) return { status: 'cancelled' };

  // 用户选择继续后排队等待原标签页释放锁，再在锁内执行，绝不无锁并行。
  result = await manager.request(lockName, { mode: 'exclusive' }, async lock => {
    if (!lock) throw new Error('无法取得商品发布协调锁，请稍后重试。');
    return task();
  });
  return { status: 'completed', value: result };
};
