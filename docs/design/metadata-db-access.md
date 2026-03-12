# Metadata.db 访问设计方案

## 背景

containerd 使用 BoltDB (metadata.db) 存储 snapshot 的 internal ID。我们需要读取这些信息来构建 snapshot 的完整路径。

**路径格式**:
```
/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/<internal_id>/fs
```

**问题**: BoltDB 使用 flock 独占锁，即使只读打开也会被阻塞或失败。

---

## 方案对比

### 方案 1: 直接打开 (当前实现)

```go
db, err := bolt.Open("meta.db", 0400, &bolt.Options{
    ReadOnly: true,
    Timeout:  2 * time.Second,
})
```

**优点**:
- 实现简单
- 数据实时

**缺点**:
- 需要获取锁，可能被 containerd 阻塞
- 2 秒超时后失败

**结论**: 作为首选方案，失败时降级到备用方案。

---

### 方案 2: 使用 Mounts API

通过 containerd 的 `Snapshotter.Mounts()` 获取挂载信息。

```go
mounts, _ := snapshotter.Mounts(ctx, key)
// 解析 upperdir=/path/to/snapshot
```

**优点**:
- 官方 API，稳定
- 不需要访问 metadata.db

**缺点**:
- 只适用于 active snapshots
- 对 committed snapshots 可能无法获取路径

**结论**: 作为 metadata.db 失败时的 fallback。

---

### 方案 3: 内存副本 (memfd_create)

创建内存中的文件副本，bbolt 打开副本而非原文件。

```go
// 1. 打开原文件（不持有锁）
origFd, _ := os.Open("meta.db")

// 2. 创建 memfd
memFd, _ := unix.MemfdCreate("meta", 0)

// 3. 零拷贝传输
unix.Sendfile(memFd, origFd, nil, size)
origFd.Close()

// 4. bbolt 打开 memfd
db, _ := bolt.Open(fmt.Sprintf("/proc/self/fd/%d", memFd), 0400, opts)
```

**优点**:
- 完全避免锁竞争
- 内存访问速度快

**缺点**:
- 需要复制整个 db 到内存
- 内存占用 = metadata.db 大小（可能几百 MB）
- 数据非实时（复制时的快照）

**结论**: 过于复杂，不建议使用。

---

### 方案 4: 磁盘副本

启动时复制 metadata.db 到临时位置。

```go
// cp /var/lib/containerd/.../meta.db /tmp/cray-meta.db
db, _ := bolt.Open("/tmp/cray-meta.db", 0400, opts)
```

**优点**:
- 简单可靠
- 完全避免锁

**缺点**:
- 占用额外磁盘空间
- 数据非实时
- 需要定期刷新副本

**结论**: 可作为备选方案。

---

### 方案 5: Mmap 直接访问（不推荐）

直接 mmap metadata.db 文件，绕过 bbolt 解析。

```go
// 使用 golang.org/x/exp/mmap 或其他库
mmap, _ := syscall.Mmap(fd, 0, size, syscall.PROT_READ, syscall.MAP_PRIVATE)
```

**关键问题**:

#### 1. 是否会修改原文件？

**答案**: 取决于 mmap 的 flags

```go
// MAP_SHARED - 修改会写回原文件（危险！）
syscall.MAP_SHARED

// MAP_PRIVATE - Copy-on-Write，不会写回原文件（安全）
syscall.MAP_PRIVATE
```

使用 `MAP_PRIVATE` 时：
- 读取：访问磁盘文件内容
- 写入：触发 Copy-on-Write，修改发生在内存副本
- **不会写回原文件**

#### 2. 为什么这个方案不推荐？

| 问题 | 说明 |
|------|------|
| 格式复杂 | BoltDB 是 B+ 树结构，需要完整解析器 |
| 版本兼容 | BoltDB 格式可能变化 |
| 锁冲突 | 即使 mmap 不修改，containerd 可能正在写入 |
| 数据一致 | mmap 看到的是文件某个时刻的快照 |

**风险**: 即使使用 `MAP_PRIVATE`，如果 containerd 正在写入，mmap 可能读到不一致的数据（部分旧数据 + 部分新数据）。

---

## 推荐架构

```
首选: 直接打开 metadata.db (2秒超时)
    ↓ 失败
备用: 使用 Mounts API
    ↓ 失败
降级: 不显示 snapshot path
```

### 代码结构

```go
// 尝试读取 snapshot path
func (r *Runtime) getSnapshotPath(key string) string {
    // 1. 尝试 metadata.db
    if r.metadataReader != nil {
        if path, err := r.metadataReader.Resolve(key); err == nil {
            return path
        }
    }

    // 2. 尝试 Mounts API
    if path, err := r.GetSnapshotPathFromMounts(key); err == nil {
        return path
    }

    // 3. 返回空
    return ""
}
```

---

## 总结

| 方案 | 复杂度 | 实时性 | 内存占用 | 推荐度 |
|------|--------|--------|----------|--------|
| 直接打开 | 低 | 是 | 低 | ⭐⭐⭐ |
| Mounts API | 低 | 是 | 低 | ⭐⭐⭐ |
| 内存副本 | 高 | 否 | 高 | ⭐ |
| 磁盘副本 | 中 | 否 | 中 | ⭐⭐ |
| Mmap | 极高 | 是 | 低 | ❌ |

**最终建议**: 使用"直接打开 + Mounts API fallback"的架构，简单可靠，覆盖绝大多数场景。
