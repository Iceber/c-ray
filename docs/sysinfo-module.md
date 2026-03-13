# System Information Collection Module

## 概述

系统信息采集模块提供了读取 Linux 系统信息的功能，包括 CGroup、procfs、挂载信息和进程树构建。

## 模块组成

### 1. CGroup Reader (`cgroup.go`)

读取 CGroup v1 和 v2 的资源限制信息。

**功能**:
- 自动检测 CGroup 版本
- 读取 CPU 限制 (quota, period, shares)
- 读取内存限制和使用量
- 读取 PIDs 限制
- 读取 Block I/O 权重

**API**:
```go
reader, err := sysinfo.NewCGroupReader()
limits, err := reader.ReadCGroupLimits("/kubepods/pod123/container456")
```

**支持的 CGroup 参数**:

| CGroup v1 | CGroup v2 | 字段 |
|-----------|-----------|------|
| cpu.cfs_quota_us | cpu.max | CPUQuota |
| cpu.cfs_period_us | cpu.max | CPUPeriod |
| cpu.shares | cpu.weight | CPUShares |
| memory.limit_in_bytes | memory.max | MemoryLimit |
| memory.usage_in_bytes | memory.current | MemoryUsage |
| pids.max | pids.max | PidsLimit |
| pids.current | pids.current | PidsCurrent |
| blkio.weight | io.weight | BlkioWeight |

### 2. Proc Reader (`procfs.go`)

读取 /proc 文件系统中的进程信息。

**功能**:
- 读取进程状态 (/proc/[pid]/stat)
- 读取命令行 (/proc/[pid]/cmdline)
- 读取内存使用 (/proc/[pid]/status)
- 读取 I/O 统计 (/proc/[pid]/io)
- 列出所有 PIDs

**API**:
```go
reader := sysinfo.NewProcReader()
process, err := reader.ReadProcess(1234)
pids, err := reader.ListPIDs()
```

**容器进程读取**:
```go
// 读取容器内进程 (通过 /proc/[pid]/root/proc)
containerReader := sysinfo.NewProcReaderWithRoot("/proc/1234/root/proc")
pids, err := containerReader.ListPIDs()
```

### 3. Mount Reader (`mount.go`)

读取和解析挂载信息。

**功能**:
- 读取 /proc/[pid]/mountinfo
- 解析 overlayfs 挂载选项
- 提取 lowerdir, upperdir, workdir
- 过滤特定类型的挂载
- 查找根挂载

**API**:
```go
reader := sysinfo.NewMountReader()
mounts, err := reader.ReadMounts(1234)

// 解析 overlayfs
lowerdir, upperdir, workdir := reader.ParseOverlayFS(mount)
layers := reader.GetOverlayLayers(mount)

// 查找根挂载
rootMount := reader.FindRootMount(mounts)
```

### 4. Process Tree (`process.go`)

构建和管理进程树。

**功能**:
- 构建进程父子关系
- 获取根进程
- 进程排序和过滤
- 收集容器进程信息

**API**:
```go
collector, err := sysinfo.NewProcessCollector()

// 收集容器进程
processes, err := collector.CollectContainerProcesses(containerPID)

// 收集 Top 信息
top, err := collector.CollectProcessTop(containerPID)

// 构建进程树
tree := sysinfo.BuildProcessTree(processes)
roots := tree.GetRootProcesses()
```

**工具函数**:
```go
// 过滤进程
filtered := sysinfo.FilterProcesses(processes, func(p *models.Process) bool {
    return p.MemoryRSS > 1024*1024 // > 1MB
})

// 排序
sysinfo.SortProcessesByMemory(processes)
sysinfo.SortProcessesByIO(processes)
```

## 集成到 Containerd Runtime

系统信息采集已集成到 containerd runtime：

```go
// GetContainerDetail 现在包含:
- CGroup 信息 (limits, version)
- 进程数量

// GetContainerStorageInfo 现在包含:
- 完整的挂载信息

// GetContainerProcesses
processes, err := runtime.GetContainerProcesses(ctx, containerID)

// GetContainerTop
top, err := runtime.GetContainerTop(ctx, containerID)

// GetContainerMounts
mounts, err := runtime.GetContainerMounts(ctx, containerID)
```

## 测试

运行测试:
```bash
go test ./pkg/sysinfo/ -v
```

**注意**: 大部分测试需要 Linux 环境和 /proc 文件系统。在 macOS 上会自动跳过。

测试覆盖:
- CGroup 版本检测
- 内存大小解析
- Overlayfs 解析
- 进程树构建
- 挂载信息过滤

## 平台兼容性

| 功能 | Linux | macOS | Windows |
|------|-------|-------|---------|
| CGroup | ✅ | ❌ | ❌ |
| Procfs | ✅ | ❌ | ❌ |
| Mount | ✅ | ❌ | ❌ |

**说明**: 此模块专为 Linux 容器环境设计。在非 Linux 平台上，相关功能会返回错误或空结果。

## 性能考虑

1. **缓存**: 不实现缓存，由上层决定
2. **错误处理**: 非致命错误不中断流程
3. **权限**: 某些操作需要 root 权限 (如读取其他用户进程的 /proc/[pid]/io)
4. **容器隔离**: 通过 /proc/[pid]/root/proc 访问容器内进程

## 未来扩展

- [ ] CPU 使用率计算 (需要采样)
- [ ] 网络统计信息
- [ ] 磁盘使用统计
- [ ] CGroup v2 完整支持
- [ ] 性能优化和缓存

## 代码统计

- 实现代码: 868 行
- 测试代码: 271 行
- 总计: 1139 行
