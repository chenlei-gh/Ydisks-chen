import { describe, expect, it, vi } from 'vitest';
import { emptyPublishItemForm } from './itemActions';
import { getSinglePublishTabLockName, runSinglePublishWithTabLock, type PublishLockManager } from './singlePublishLock';

describe('single publish tab lock', () => {
  it('为相同账号和商品内容生成稳定锁名', () => {
    // form 是用于验证稳定指纹的普通商品表单。
    const form = { ...emptyPublishItemForm('account-1'), title: '同一商品' };
    expect(getSinglePublishTabLockName(form, null, null)).toBe(getSinglePublishTabLockName(form, null, null));
    expect(getSinglePublishTabLockName(form, null, null)).not.toBe(
      getSinglePublishTabLockName({ ...form, title: '另一商品' }, null, null),
    );
    expect(getSinglePublishTabLockName(form, null, null)).not.toBe(
      getSinglePublishTabLockName({ ...form, cookie_id: 'account-2' }, null, null),
    );
  });

  it('锁空闲时直接在锁内执行发布任务', async () => {
    // task 是本次发布动作。
    const task = vi.fn(async () => 'published');
    // manager 模拟 Web Locks 在锁空闲时立即授予锁。
    const manager: PublishLockManager = {
      request: async (_name, _options, callback) => callback({} as Lock),
    };
    const result = await runSinglePublishWithTabLock('lock', task, () => false, manager);
    expect(result).toEqual({ status: 'completed', value: 'published' });
    expect(task).toHaveBeenCalledTimes(1);
  });

  it('检测到冲突并由用户取消时不执行发布', async () => {
    // task 是不应执行的发布动作。
    const task = vi.fn(async () => 'published');
    // manager 模拟锁已被其他标签页占用。
    const manager: PublishLockManager = {
      request: async (_name, options, callback) => callback(options.ifAvailable ? null : ({} as Lock)),
    };
    const result = await runSinglePublishWithTabLock('lock', task, () => false, manager);
    expect(result).toEqual({ status: 'cancelled' });
    expect(task).not.toHaveBeenCalled();
  });

  it('用户选择继续后等待锁释放并只执行一次', async () => {
    // task 是等待锁释放后执行的发布动作。
    const task = vi.fn(async () => 'published');
    // requests 记录先探测冲突、再排队等待的两次请求。
    const requests: boolean[] = [];
    const manager: PublishLockManager = {
      request: async (_name, options, callback) => {
        requests.push(Boolean(options.ifAvailable));
        return callback(options.ifAvailable ? null : ({} as Lock));
      },
    };
    const result = await runSinglePublishWithTabLock('lock', task, () => true, manager);
    expect(result).toEqual({ status: 'completed', value: 'published' });
    expect(requests).toEqual([true, false]);
    expect(task).toHaveBeenCalledTimes(1);
  });

  it('浏览器不支持 Web Locks 时兼容原发布流程', async () => {
    // task 是兼容模式下继续执行的发布动作。
    const task = vi.fn(async () => 'published');
    const result = await runSinglePublishWithTabLock('lock', task, () => false, undefined);
    expect(result).toEqual({ status: 'completed', value: 'published' });
    expect(task).toHaveBeenCalledTimes(1);
  });
});
