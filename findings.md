# Findings: c-ray 项目研究记录

## 项目背景

c-ray 是一个容器管理命令行 TUI 工具，目标是提供类似 htop 的交互式界面来管理和监控容器。

## Containerd 架构理解

### 1. Content Store (内容存储)

**位置**: `/var/lib/containerd/io.containerd.content.v1.content/blobs/`

**存储内容**:
- 镜像 Index (manifest list)
- 镜像 Manifest (每个平台一个)
- 镜像 Config (JSON 配置文件)
- 镜像 Layers (tar.gz 格式的层)

**关键特性**:
- 所有内容按 SHA256 哈希存储
- 使用 labels 进行 GC 引用管理
- 内容不可变

**Labels 系统**:
- `containerd.io/gc.ref.content.config`: 引用配置文件
- `containerd.io/gc.ref.content.l.<index>`: 引用层
- `containerd.io/gc.ref.content.m.<index>`: 引用 manifest
- `containerd.io/uncompressed`: 未压缩内容的哈希

### 2. Snapshotter (快照管理)

**位置**: `/var/lib/containerd/io.containerd.snapshotter.v1.<type>/`
- 例如 overlayfs: `/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/`

**快照类型**:
- **Committed**: 不可变的层快照，对应镜像的每一层
- **Active**: 可变的容器根文件系统

**工作流程**:
1. 从 content store 读取层 blob (tar.gz)
2. 创建 active snapshot
3. diff applier 应用层内容到 snapshot
4. commit snapshot 变为 committed
5. 下一层以此为 parent 继续

**关键点**:
- 每个镜像层对应一个 committed snapshot
- 每个容器有一个 active snapshot
- Snapshot 名称是应用层后的内容哈希，不是原始 blob 哈希
- 根层的 snapshot 名称 = 第一层 blob 的 uncompressed 哈希

### 3. Mount Management (挂载管理)

**核心概念**:
- `Mount` 类型: 延迟挂载的文件系统描述
- Mount Manager: 扩展挂载功能，支持自定义挂载类型

**Mount Handlers**:
- `loop`: Loopback 设备挂载
- 可插拔的自定义 handler

**Mount Transformers**:
- `format/`: 模板化挂载参数 (使用 go template)
- `mkfs/`: 创建和格式化文件系统镜像
- `mkdir/`: 创建目录
- 可链式组合: `mkfs/loop`, `format/mkdir/overlay`

**Format Transformer 模板变量**:
- `{{ source <index> }}`: 引用前一个挂载的 source
- `{{ target <index> }}`: 引用前一个挂载的 target
- `{{ mount <index> }}`: 引用前一个挂载点
- `{{ overlay <start> <end> }}`: 生成 overlayfs lowerdir 参数

**GC 集成**:
- 使用 `containerd.io/gc.bref.*` labels 进行反向引用
- 支持引用: container, content, image, snapshot

### 4. 容器创建流程

1. **Pull**: 下载镜像到 content store
2. **Unpack**: 解压层到 snapshots (创建 committed snapshots)
3. **Prepare**: 创建 active snapshot (基于顶层 committed snapshot)
4. **Create Container**: 使用 active snapshot 作为 rootfs

## 技术选型

### TUI 框架: tview

**优势**:
- 成熟稳定，广泛使用
- 丰富的组件: Table, TreeView, TextView, Flex 等
- 良好的布局系统
- 支持鼠标和键盘交互

**核心组件**:
- `Application`: 主应用
- `Pages`: 页面管理器
- `Table`: 列表展示
- `TreeView`: 树状展示
- `Flex`: 布局容器
- `TextView`: 文本展示

### Containerd Client API

**关键接口**:
- `client.New()`: 创建客户端
- `client.Containers()`: 获取容器列表
- `client.Images()`: 获取镜像列表
- `container.Info()`: 容器元数据
- `container.Task()`: 容器任务 (进程)
- `container.Spec()`: OCI 规范
- `client.SnapshotService()`: 快照服务

### 系统信息采集

**Procfs**:
- `/proc/[pid]/stat`: 进程状态
- `/proc/[pid]/status`: 详细状态
- `/proc/[pid]/io`: I/O 统计
- `/proc/[pid]/children`: 子进程
- `/proc/[pid]/cmdline`: 命令行

**CGroup**:
- v1: `/sys/fs/cgroup/[controller]/[path]`
- v2: `/sys/fs/cgroup/[path]`
- 需要检测版本并适配

**Mount Info**:
- `/proc/[pid]/mountinfo`: 挂载信息
- 解析 overlayfs 的 lowerdir, upperdir, workdir

## 架构设计要点

### 1. 运行时抽象

**接口设计原则**:
- 定义通用的 Runtime 接口
- 隔离 containerd 特定实现
- 为 cri-o, docker 预留扩展点

**核心接口**:
```go
type Runtime interface {
    ListContainers(ctx context.Context) ([]Container, error)
    ListImages(ctx context.Context) ([]Image, error)
    GetContainerDetail(ctx context.Context, id string) (*ContainerDetail, error)
    // ...
}
```

### 2. 数据模型

**Container**:
- ID, Name, Image, Status, CreatedAt, StartedAt
- PID, Labels (用于提取 Pod 信息)

**Image**:
- Name, Digest, Size, CreatedAt
- Layers (引用 snapshot)

**Pod** (从 Container labels 提取):
- Name, Namespace, UID
- Containers (关联的容器列表)

**Process**:
- PID, PPID, Command, Args
- CPU%, Memory%, IO
- Children (进程树)

### 3. UI 架构

**分层**:
- Application Layer: 主应用和页面管理
- View Layer: 各个视图组件
- Component Layer: 可复用的 UI 组件
- Data Layer: 数据获取和转换

**导航流程**:
```
Main List (Containers/Images/Pods)
  ↓ (选择容器)
Container Detail
  ├─ Overview (上半部分)
  └─ Tabs (下半部分)
      ├─ Top
      ├─ Process Tree
      ├─ Mounts
      ├─ Image Layers
      └─ Runtime Info
```

## 实现难点与解决方案

### 1. PID Namespace 处理

**问题**: 容器内的 PID 与宿主机 PID 不同

**解决方案**:
- 使用 container.Task().Pid() 获取宿主机 PID
- 通过 `/proc/[pid]/root` 访问容器内文件系统
- 读取 `/proc/[pid]/root/proc` 获取容器内进程信息

### 2. CGroup v1/v2 兼容

**问题**: 不同系统使用不同 cgroup 版本

**解决方案**:
- 检测 `/sys/fs/cgroup/cgroup.controllers` 是否存在判断版本
- 实现两套读取逻辑
- 统一数据模型

### 3. Overlayfs 层解析

**问题**: 需要理解 overlayfs 的层级结构

**解决方案**:
- 读取 `/proc/[pid]/mountinfo` 获取挂载参数
- 解析 lowerdir (多层用 `:` 分隔)
- 关联到 snapshotter 的 committed snapshots
- 参考 containerd 的 mount management 文档

### 4. 实时数据更新

**问题**: 平衡刷新频率和性能

**解决方案**:
- 不同视图使用不同刷新间隔
  - 列表: 2-3 秒
  - Top: 1 秒
  - 静态信息: 不刷新
- 使用 goroutine + channel 异步更新
- 实现数据缓存，避免重复查询

### 5. 大数据量渲染

**问题**: 大量容器/进程时 UI 卡顿

**解决方案**:
- 虚拟滚动 (tview 内置支持)
- 分页加载
- 延迟渲染非可见区域
- 使用 sync.Pool 复用对象

## 参考资料

- containerd 官方文档: https://github.com/containerd/containerd/tree/main/docs
- tview 文档: https://github.com/rivo/tview
- OCI Runtime Spec: https://github.com/opencontainers/runtime-spec
- procfs 格式: https://man7.org/linux/man-pages/man5/proc.5.html

## 下一步行动

1. 创建项目目录结构
2. 初始化 go.mod
3. 定义核心接口
4. 实现 containerd 客户端基础连接
5. 搭建 tview 基础框架
