import { describe, expect, it, vi } from 'vitest';
import { emptyPublishItemForm } from './itemActions';
import { getSinglePublishTabLockName, runSinglePublishWithTabLock, type PublishLockManager } from './singlePublishLock';

describe('single publish tab lock', /* suiteCallback 定义单商品发布标签页协调测试组。 */ () => {
  it('为相同账号和商品内容生成稳定锁名', /* stableNameTest 验证相同内容稳定且账号隔离。 */ () => {
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

  it('锁空闲时直接在锁内执行发布任务', /* availableLockTest 验证锁空闲时执行一次任务。 */ async () => {
    // task 是本次发布动作。
    const task = vi.fn(/* publishTask 模拟一次发布操作。 */ async () => 'published');
    // manager 模拟 Web Locks 在锁空闲时立即授予锁。
    // manager 模拟浏览器先报告冲突、后授予排队锁。\n    const manager: PublishLockManager = {
      request: /* availableRequest 模拟浏览器立即授予锁。 */ async (_name, _options, callback) => callback({} as Lock),
    };
    // result 保存锁空闲时的协调执行结果。\n    const result = await runSinglePublishWithTabLock('lock', task, /* noConflictConfirm 无冲突时不需要确认。 */ () => false, manager);
    expect(result).toEqual({ status: 'completed', value: 'published' });
    expect(task).toHaveBeenCalledTimes(1);
  });

  it('检测到冲突并由用户取消时不执行发布', /* cancelConflictTest 验证用户取消后不发送发布请求。 */ async () => {
    // task 是不应执行的发布动作。
    const task = vi.fn(/* publishTask 模拟等待锁后执行的发布操作。 */ async () => 'published');
    // manager 模拟锁已被其他标签页占用。
    const manager: PublishLockManager = {
      request: /* occupiedRequest 模拟首次探测发现其他标签页持有锁。 */ async (_name, options, callback) => callback(options.ifAvailable ? null : ({} as Lock)), 
    };
    // result 保存用户取消冲突发布后的结果。\n    const result = await runSinglePublishWithTabLock('lock', task, /* cancelConflictConfirm 模拟用户选择取消。 */ () => false, manager);
    expect(result).toEqual({ status: 'cancelled' });
    expect(task).not.toHaveBeenCalled();
  });

  it('用户选择继续后等待锁释放并只执行一次', /* continueConflictTest 验证继续操作会排队等待锁。 */ async () => {
    // task 是等待锁释放后执行的发布动作。
    const task = vi.fn(async () => 'published');
    // requests 记录先探测冲突、再排队等待的两次请求。
    const requests: boolean[] = [];
    const manager: PublishLockManager = {
      request: /* queuedRequest 记录首次探测和后续排队请求。 */ async (_name, options, callback) => {
        requests.push(Boolean(options.ifAvailable));
        return callback(options.ifAvailable ? null : ({} as Lock));
      },
    };
    // result 保存用户确认继续后的协调执行结果。\n    const result = await runSinglePublishWithTabLock('lock', task, /* continueConflictConfirm 模拟用户选择继续。 */ () => true, manager);
    expect(result).toEqual({ status: 'completed', value: 'published' });
    expect(requests).toEqual([true, false]);
    expect(task).toHaveBeenCalledTimes(1);
  });

  it('浏览器不支持 Web Locks 时兼容原发布流程', /* unsupportedLockTest 验证浏览器兼容回退。 */ async () => {
    // task 是兼容模式下继续执行的发布动作。
    const task = vi.fn(async () => 'published');
    // result 保存不支持 Web Locks 时的兼容执行结果。\n    const result = await runSinglePublishWithTabLock('lock', task, /* fallbackConfirm 在兼容模式不应被调用。 */ () => false, undefined);
    expect(result).toEqual({ status: 'completed', value: 'published' });
    expect(task).toHaveBeenCalledTimes(1);
  });
});
