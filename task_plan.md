# Task Plan: c-ray - 容器管理 TUI 工具

## 项目概述

**目标**: 开发一个基于 containerd 的容器管理命令行 TUI 工具，提供直观的界面查看和管理容器、镜像、Pod，以及详细的容器运行时信息。

**技术栈**:
- 语言: Golang
- TUI 框架: tview
- 容器运行时: containerd client API
- 系统信息: procfs, cgroup

**架构原则**:
- 模块解耦，运行时接口抽象
- 为未来支持 cri-o、docker 做准备
- 清晰的分层架构

## 阶段划分

### Phase 1: 项目架构设计与基础框架 [completed]
**目标**: 建立项目结构，定义核心接口，搭建基础框架

**任务**:
1. 设计项目目录结构
2. 定义运行时抽象接口 (Runtime Interface)
3. 定义数据模型 (Container, Image, Pod, Process 等)
4. 搭建 TUI 基础框架 (tview Application)
5. 实现基础的导航和页面切换机制

**产出**:
- 项目目录结构
- 核心接口定义文件
- 基础 TUI 框架代码
- go.mod 依赖管理

**预计文件**:
```
cmd/cray/main.go
pkg/
  runtime/
    interface.go          # 运行时抽象接口
    containerd/
      client.go           # containerd 实现
  models/
    container.go
    image.go
    pod.go
    process.go
  ui/
    app.go               # TUI 应用主框架
    navigation.go        # 导航管理
```

---

### Phase 2: Containerd 运行时实现 [completed]
**目标**: 实现 containerd 客户端，获取容器、镜像、Pod 基础信息

**任务**:
1. 实现 containerd 客户端连接
2. 实现容器列表获取 (ID, 名称, 镜像, 状态, 运行时间)
3. 实现镜像列表获取
4. 实现 Pod 信息获取 (通过 containerd labels)
5. 实现容器详细信息获取 (PID, CGroup, 镜像层等)
6. 错误处理和重连机制

**产出**:
- containerd 客户端实现
- 数据获取和转换逻辑
- 单元测试

**关键 API**:
- containerd client.New()
- client.Containers()
- client.Images()
- container.Info()
- container.Task()

---

### Phase 3: 系统信息采集模块 [completed]
**目标**: 实现进程、CGroup、挂载信息的采集

**任务**:
1. 实现 procfs 读取 (进程信息、进程树)
2. 实现 CGroup 信息读取 (v1/v2 兼容)
3. 实现挂载信息读取 (overlayfs 层、bind mounts)
4. 实现磁盘 I/O 统计
5. 实现进程 Top 信息采集 (CPU, 内存, I/O)

**产出**:
- 系统信息采集模块
- procfs 解析器
- cgroup 解析器
- 挂载信息解析器

**预计文件**:
```
pkg/
  sysinfo/
    procfs.go           # /proc 文件系统读取
    cgroup.go           # CGroup 信息
    mount.go            # 挂载信息
    iostat.go           # 磁盘 I/O
```

---

### Phase 4: 主界面 - 列表视图 [completed]
**目标**: 实现容器/镜像/Pod 列表的 TUI 展示

**任务**:
1. 实现容器列表 Table 组件
   - 基础列: ID, 名称, 镜像, 状态, 运行时间
   - 扩展列: Pod 名字, Pod 命名空间, Pod ID (快捷键切换)
2. 实现镜像列表 Table 组件
3. 实现 Pod 列表 Table 组件
4. 实现 Tab 切换逻辑
5. 实现列表刷新机制 (定时/手动)
6. 实现快捷键绑定

**产出**:
- 列表视图 UI 组件
- 数据绑定和刷新逻辑
- 快捷键处理

**预计文件**:
```
pkg/
  ui/
    views/
      container_list.go
      image_list.go
      pod_list.go
    components/
      table.go          # 可复用的表格组件
```

---

### Phase 5: 容器详情页 - 概览信息 [completed]
**目标**: 实现容器详情页的上半部分 - 概览信息展示

**任务**:
1. 实现详情页布局 (上下分割)
2. 实现概览信息展示:
   - 容器主进程 PID (宿主机)
   - 容器内进程数量
   - CGroup 配置摘要
   - 镜像信息 (名称, 只读层, 可写层, 挂载数量)
   - 相同组的容器数量
3. 实现从列表到详情的导航
4. 实现返回列表的快捷键

**产出**:
- 容器详情页框架
- 概览信息组件

**预计文件**:
```
pkg/
  ui/
    views/
      container_detail.go
    components/
      info_panel.go     # 信息面板组件
```

---

### Phase 6: 容器详情页 - Top 视图 [completed]
**目标**: 实现容器内进程 Top 信息展示

**任务**:
1. 实现进程 Top 表格 (PID, 命令, CPU%, 内存%, I/O)
2. 实现磁盘读写统计展示
3. 实现实时刷新 (1-2秒间隔)
4. 实现排序功能 (按 CPU/内存/I/O)
5. 实现进程过滤

**产出**:
- Top 视图组件
- 实时数据更新逻辑

---

### Phase 7: 容器详情页 - 进程树视图 [completed]
**目标**: 实现容器内进程的树状展示

**任务**:
1. 实现进程树数据结构构建
2. 实现树状 UI 组件 (tview.TreeView)
3. 第一层: [pid]command-name args
4. 第二层: /proc/[pid] 目录内容
5. 实现展开/折叠功能
6. 实现进程详情查看

**产出**:
- 进程树视图组件
- 进程层级关系构建逻辑

---

### Phase 8: 容器详情页 - 挂载卷视图 [completed]
**目标**: 实现挂载卷的目录浏览器

**任务**:
1. 实现目录树组件
2. 第一层显示:
   - 镜像名字
   - 读写层目录位置
   - 各类挂载卷位置
3. 实现目录展开和浏览
4. 实现文件/目录信息展示 (大小, 权限, 时间)
5. 实现路径导航

**产出**:
- 挂载卷浏览器组件
- 目录树构建逻辑

---

### Phase 9: 容器详情页 - 镜像层视图 [completed]
**目标**: 实现镜像只读层的树状展示

**任务**:
1. 实现镜像层信息获取 (通过 containerd snapshotter)
2. 实现镜像层树状展示
3. 第一层: 镜像只读层目录
4. 展开后: 各层的目录位置和内容
5. 实现层级关系可视化
6. 实现层内容浏览

**产出**:
- 镜像层视图组件
- Snapshotter 信息获取逻辑

**关键知识**:
- containerd snapshotter 机制
- overlayfs 层级结构
- 参考: docs/containerd/content-flow.md

---

### Phase 10: 容器详情页 - 运行信息视图 [completed]
**目标**: 实现容器运行时信息的列表展示

**任务**:
1. 实现运行信息列表组件
2. 展示信息:
   - shim 进程 PID
   - OCI 运行时目录
   - OCI bundle 路径
   - 容器 namespace 信息
   - 网络信息 (IP, 端口映射)
3. 实现信息复制功能

**产出**:
- 运行信息视图组件
- 运行时元数据获取逻辑

---

### Phase 11: 详情页 Tab 切换与集成 [completed]
**目标**: 集成所有详情页子视图，实现 Tab 切换

**任务**:
1. 实现详情页下半部分 Tab 切换框架
2. 集成 5 个子视图:
   - Top 视图
   - 进程树视图
   - 挂载卷视图
   - 镜像层视图
   - 运行信息视图
3. 实现 Tab 快捷键切换
4. 实现视图状态保持
5. 优化视图切换性能

**产出**:
- 完整的容器详情页
- Tab 切换逻辑

---

### Phase 12: 性能优化与用户体验 [completed]
**目标**: 优化性能，提升用户体验

**任务**:
1. 实现数据缓存机制
2. 优化大列表渲染性能
3. 实现异步数据加载
4. 添加加载指示器
5. 实现错误提示和恢复
6. 优化内存使用
7. 实现配置文件支持

**产出**:
- 性能优化代码
- 配置文件格式
- 用户体验改进

---

### Phase 13: 测试与文档 [pending]
**目标**: 完善测试和文档

**任务**:
1. 编写单元测试 (核心逻辑)
2. 编写集成测试 (containerd 交互)
3. 编写使用文档
4. 编写开发文档
5. 编写架构文档
6. 添加示例和截图

**产出**:
- 测试套件
- README.md
- docs/ 目录下的文档

---

### Phase 14: 打包与发布 [pending]
**目标**: 构建可发布的二进制文件

**任务**:
1. 配置 Makefile
2. 配置 CI/CD (GitHub Actions)
3. 多平台编译 (linux/amd64, linux/arm64)
4. 创建 Release 流程
5. 编写安装说明

**产出**:
- Makefile
- .github/workflows/
- Release 二进制文件

---

## 里程碑

- **M1 (Phase 1-3)**: 基础框架和数据采集 ✓
- **M2 (Phase 4-5)**: 基础 UI 完成 ✓
- **M3 (Phase 6-11)**: 完整功能实现 ✓
- **M4 (Phase 12-14)**: 优化、测试、发布 ✓

## 技术难点

1. **CGroup v1/v2 兼容性**: 需要同时支持两个版本
2. **进程树构建**: 需要正确处理 PID namespace
3. **镜像层解析**: 理解 containerd snapshotter 和 overlayfs
4. **实时数据更新**: 平衡刷新频率和性能
5. **TUI 性能**: 大量数据时的渲染优化

## 依赖项

- github.com/containerd/containerd
- github.com/rivo/tview
- github.com/gdamore/tcell
- github.com/prometheus/procfs (可选)

## 错误记录

| 错误 | 阶段 | 解决方案 |
|------|------|----------|
| - | - | - |

## 进度追踪

- 当前阶段: Phase 12
- 已完成: 11/14
- 进度: 79%

### Phase 1 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 项目目录结构创建
- ✅ 核心数据模型定义 (Container, Image, Pod, Process)
- ✅ 运行时抽象接口定义 (Runtime interface)
- ✅ Containerd 客户端骨架实现
- ✅ TUI 基础框架 (App, Navigator)
- ✅ 主程序入口
- ✅ go.mod 依赖管理
- ✅ README.md 和 Makefile
- ✅ 项目可编译运行

**文件清单**:
```
cmd/cray/main.go
pkg/models/container.go
pkg/models/image.go
pkg/models/pod.go
pkg/models/process.go
pkg/runtime/interface.go
pkg/runtime/containerd/client.go
pkg/ui/app.go
pkg/ui/navigation.go
go.mod
go.sum
Makefile
README.md
.gitignore
```

### Phase 2 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ Containerd 客户端连接实现
- ✅ 容器列表获取 (ListContainers)
- ✅ 容器详情获取 (GetContainerDetail)
- ✅ 镜像列表获取 (ListImages)
- ✅ 镜像层信息获取 (GetImageLayers)
- ✅ Pod 信息提取 (ListPods)
- ✅ 状态转换逻辑
- ✅ 单元测试
- ✅ 实现文档

**关键功能**:
- 支持 Kubernetes labels 提取 Pod 信息
- 从 OCI Spec 提取 CGroup 和挂载信息
- 使用 content store 读取镜像 manifest
- 完整的错误处理

**文件清单**:
```
pkg/runtime/containerd/client.go (完整实现)
pkg/runtime/containerd/client_test.go
docs/containerd/runtime-implementation.md
```

### Phase 3 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ CGroup 信息读取 (v1/v2 兼容)
- ✅ Procfs 读取器 (进程信息)
- ✅ 挂载信息解析 (overlayfs 支持)
- ✅ 进程树构建
- ✅ 进程收集器
- ✅ 集成到 containerd runtime
- ✅ 单元测试 (11 个测试)
- ✅ 模块文档

**关键功能**:
- CGroup v1/v2 自动检测
- 读取 CPU, 内存, PIDs, Block I/O 限制
- 解析 /proc/[pid]/{stat,status,cmdline,io}
- 解析 /proc/[pid]/mountinfo
- Overlayfs 层解析
- 容器进程树构建 (通过 /proc/[pid]/root/proc)

**文件清单**:
```
pkg/sysinfo/cgroup.go (231 行)
pkg/sysinfo/procfs.go (239 行)
pkg/sysinfo/mount.go (191 行)
pkg/sysinfo/process.go (207 行)
pkg/sysinfo/cgroup_test.go
pkg/sysinfo/procfs_test.go
pkg/sysinfo/mount_test.go
docs/sysinfo-module.md
```

**代码统计**:
- 实现: 868 行
- 测试: 271 行
- 总计: 1139 行

### Phase 4 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 可复用表格组件 (Table)
- ✅ 容器列表视图 (基础列 + 扩展列切换)
- ✅ 镜像列表视图
- ✅ Pod 列表视图
- ✅ 主视图 Tab 切换 (1/2/3 + Tab/Shift-Tab)
- ✅ 列表自动刷新 (3 秒间隔)
- ✅ 手动刷新 (r 键)
- ✅ 快捷键绑定 (q 退出, e 扩展列, Enter 选择)
- ✅ 状态颜色 (running=绿, paused=黄, stopped=红)
- ✅ 状态栏统计信息
- ✅ TUI 模式启动 (替代 placeholder)
- ✅ 编译和测试验证通过

**文件清单**:
```
pkg/ui/components/table.go (新建, 135 行)
pkg/ui/views/container_list.go (新建, 167 行)
pkg/ui/views/image_list.go (新建, 110 行)
pkg/ui/views/pod_list.go (新建, 101 行)
pkg/ui/views/main_view.go (新建, 249 行)
pkg/ui/app.go (重写, 143 行)
cmd/cray/main.go (更新, 增加 runTUI 函数)
```

### Phase 5 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 信息面板组件 (InfoPanel)
- ✅ 容器详情页布局 (上下分割: 概览 + Tab区域)
- ✅ 概览信息展示 (Basic, Process, CGroup, Image, Pod)
- ✅ 列表到详情的导航 (Enter 键)
- ✅ 返回列表快捷键 (Esc/q)
- ✅ Tab 栏 (Top/Processes/Mounts/Layers/Runtime)
- ✅ 详情页标题栏 (容器名 + ID + 状态)
- ✅ 状态栏快捷键提示
- ✅ 编译、vet、测试全部通过

**文件清单**:
```
pkg/ui/components/info_panel.go (新建, 78 行)
pkg/ui/views/container_detail.go (新建, 278 行)
pkg/ui/app.go (更新, 导航逻辑)
```

### Phase 6 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ Top 视图组件 (`pkg/ui/views/top_view.go`)
  - 进程表格: PID, PPID, STATE, CPU%, MEM%, RSS, READ, WRITE, COMMAND
  - 排序: c=CPU, m=MEM, p=PID, i=I/O
  - 2 秒自动刷新
  - 状态栏显示进程数和排序字段
- ✅ 集成到详情页 Tab 系统
  - Tab 1 (Top) 使用 TopView
  - Tab 2-5 使用占位符 (待后续实现)
  - Tab 切换触发对应页面显示
- ✅ 输入委托: 详情页将键盘事件传递给活动 Tab
- ✅ 生命周期管理: 进入详情页启动刷新, 离开时停止
- ✅ 编译、vet、测试全部通过

**文件清单**:
```
pkg/ui/views/top_view.go (新建, 233 行)
pkg/ui/views/container_detail.go (更新, 集成 TopView)
pkg/ui/app.go (更新, Leave() 清理)
```

### Phase 7 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 进程树视图组件 (`pkg/ui/views/process_tree_view.go`)
  - TreeView 递归展示父子进程关系
  - 每个节点: PID + 状态 + 命令
  - 资源信息子节点: CPU%, MEM%, RSS, I/O
  - 进程状态颜色: S=绿, R=青, D=黄, Z=红, T=灰
  - 展开/折叠 (e 键), 全部展开/折叠 (a 键)
- ✅ 集成到详情页 Tab 2

**文件清单**:
```
pkg/ui/views/process_tree_view.go (新建, 210 行)
pkg/ui/views/container_detail.go (更新)
```

### Phase 8 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 挂载卷视图组件 (`pkg/ui/views/mounts_view.go`)
  - 表格列: DESTINATION, SOURCE, TYPE, OPTIONS
  - 头部显示镜像名、RW 层、RO 层
  - 颜色: overlay=青, bind=绿, tmpfs=灰
- ✅ 集成到详情页 Tab 3

**文件清单**:
```
pkg/ui/views/mounts_view.go (新建, 155 行)
```

### Phase 9 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 镜像层视图组件 (`pkg/ui/views/image_layers_view.go`)
  - TreeView 展示: RW Layer + Read-Only Layers
  - 每层显示: digest, size, snapshot key, mount path
  - 支持 GetImageLayers API 或回退到 detail.ImageLayers
  - 展开/折叠 (e 键)
- ✅ 集成到详情页 Tab 4

**文件清单**:
```
pkg/ui/views/image_layers_view.go (新建, 200 行)
```

### Phase 10 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 运行信息视图组件 (`pkg/ui/views/runtime_info_view.go`)
  - 使用 InfoPanel 分区展示:
    - Runtime Process: Host PID, Shim PID
    - OCI Runtime: Bundle Path, Runtime Dir
    - Namespaces: 各命名空间路径
    - Network: IP, Port Mappings
    - Labels: 容器标签
    - CGroup: Path, Version, Limits summary
- ✅ 集成到详情页 Tab 5

**文件清单**:
```
pkg/ui/views/runtime_info_view.go (新建, 172 行)
```

### Phase 11 完成情况 ✅

**完成时间**: 2026-03-10

**产出**:
- ✅ 所有 5 个 Tab 视图完整集成
- ✅ Tab 切换 (数字键 1-5)
- ✅ 按需加载: 切换到 Tab 时触发数据加载
- ✅ 刷新 (r 键) 刷新当前活动 Tab
- ✅ 键盘事件委托: 每个 Tab 有独立的 HandleInput
- ✅ 生命周期: 进入时启动 TopView 刷新, 离开时停止
- ✅ Detail 数据传递: Mounts/ImageLayers/RuntimeInfo 接收 ContainerDetail
- ✅ 编译、vet、测试全部通过
