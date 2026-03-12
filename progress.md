# Progress Log: c-ray 开发进度

## Session 1: 2026-03-10

### 初始化规划

**时间**: 14:41

**完成**:
- ✅ 阅读现有文档 (containerd mounts, content-flow)
- ✅ 创建 task_plan.md (14 个阶段的详细计划)
- ✅ 创建 findings.md (技术研究和架构设计)
- ✅ 创建 progress.md (本文件)

**理解要点**:
1. Containerd 三层架构: Content Store → Snapshots → Container
2. Mount Management 的 transformer 和 handler 机制
3. TUI 框架选择 tview，组件丰富
4. 需要处理 PID namespace, CGroup v1/v2, overlayfs 等技术难点

### Phase 1: 项目架构设计与基础框架 ✅

**时间**: 14:45 - 15:00

**完成**:
- ✅ 创建项目目录结构
- ✅ 初始化 go.mod (Go 1.24.3)
- ✅ 定义数据模型
  - Container, ContainerDetail, ContainerStatus
  - Image, ImageLayer
  - Pod
  - Process, ProcessTop
  - CGroupLimits, Mount, PortMapping
- ✅ 定义运行时抽象接口 (Runtime interface)
- ✅ 实现 Containerd 客户端骨架
- ✅ 搭建 TUI 基础框架
  - App: 主应用结构
  - Navigator: 页面导航管理
  - 全局快捷键 (q/Ctrl+C 退出)
- ✅ 实现主程序入口
- ✅ 添加依赖
  - github.com/containerd/containerd/v2
  - github.com/rivo/tview
  - github.com/gdamore/tcell/v2
- ✅ 编译验证通过
- ✅ 创建 README.md
- ✅ 创建 Makefile
- ✅ 创建 .gitignore

**产出文件**:
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
bin/cray (可执行文件)
```

**技术决策**:
- Go 版本升级到 1.24.3 (containerd v2 要求)
- 使用 GOPROXY=https://proxy.golang.org,direct 解决依赖下载问题

### Phase 2: Containerd 运行时实现 ✅

**时间**: 15:00 - 15:20

**完成**:
- ✅ 实现 containerd 客户端连接
  - Connect() 方法
  - 支持自定义 socket 路径和 namespace
- ✅ 实现容器管理
  - ListContainers(): 获取所有容器
  - GetContainer(): 获取单个容器
  - GetContainerDetail(): 获取详细信息
  - 从 labels 提取 Pod 信息
  - 获取容器状态和 PID
  - 提取 CGroup 路径和挂载信息
- ✅ 实现镜像管理
  - ListImages(): 获取所有镜像
  - GetImage(): 获取单个镜像
  - GetImageLayers(): 获取镜像层信息
  - 使用 content store 读取 manifest
- ✅ 实现 Pod 管理
  - ListPods(): 从容器 labels 提取 Pod
  - 按 Pod UID 分组容器
  - 支持 Kubernetes 标准 labels
- ✅ 状态转换
  - convertStatus(): 状态映射
  - 支持 created, running, paused, stopped
- ✅ 单元测试
  - 测试运行时创建
  - 测试状态转换
  - 测试错误处理
  - 5/6 测试通过 (1 个跳过需要 containerd)
- ✅ 编译验证通过
- ✅ 创建实现文档

**技术要点**:
- 使用 containerd v2.2.1 API
- 支持 Kubernetes labels 提取
- 使用 images.Manifest() 读取镜像层
- 完整的错误处理和包装

**遇到的问题**:
1. ProcessStatus 类型不匹配 → 使用 string() 转换
2. Image.Manifest() 方法不存在 → 使用 images.Manifest(ctx, cs, target, nil)
3. 测试失败 (containerd 未运行) → 添加 t.Skip()

### Phase 3: 系统信息采集模块 ✅

**时间**: 15:20 - 15:45

**完成**:
- ✅ 实现 CGroup 信息读取
  - 自动检测 CGroup v1/v2
  - 读取 CPU 限制 (quota, period, shares)
  - 读取内存限制和使用量
  - 读取 PIDs 限制
  - 读取 Block I/O 权重
  - 231 行代码
- ✅ 实现 Procfs 读取器
  - 读取 /proc/[pid]/stat (进程状态)
  - 读取 /proc/[pid]/cmdline (命令行)
  - 读取 /proc/[pid]/status (内存信息)
  - 读取 /proc/[pid]/io (I/O 统计)
  - 列出所有 PIDs
  - 支持自定义 proc root (容器内进程)
  - 239 行代码
- ✅ 实现挂载信息解析
  - 解析 /proc/[pid]/mountinfo
  - 解析 overlayfs 挂载选项
  - 提取 lowerdir, upperdir, workdir
  - 过滤和查找挂载
  - 191 行代码
- ✅ 实现进程树构建
  - 构建父子关系
  - 获取根进程
  - 进程收集器
  - 排序和过滤工具
  - 207 行代码
- ✅ 集成到 containerd runtime
  - GetContainerProcesses() 完整实现
  - GetContainerTop() 完整实现
  - GetContainerMounts() 完整实现
  - GetContainerDetail() 增强 (CGroup, 进程数)
- ✅ 单元测试
  - 11 个测试用例
  - 平台检测 (Linux/macOS)
  - 271 行测试代码
- ✅ 模块文档

**技术要点**:
- CGroup v1/v2 兼容性处理
- 通过 /proc/[pid]/root/proc 访问容器内进程
- Overlayfs 层解析 (lowerdir 多层支持)
- 进程树递归构建
- 平台检测和测试跳过

**代码统计**:
- 实现: 868 行
- 测试: 271 行
- 总计: 1139 行

**测试结果**:
- 11 个测试
- 6 个通过 (解析逻辑)
- 5 个跳过 (需要 Linux /proc)

### kind-control-plane 测试 ✅

**时间**: 16:30 - 16:40

**测试环境**:
- kind-control-plane 容器 (Kubernetes v1.35.1)
- containerd v2.2.1
- CGroup v2
- ARM64 架构

**测试功能**:

1. **容器列表** ✅
   - 发现 18 个容器
   - 包括 Kubernetes 组件和 Pod 容器
   - 正确提取 Pod 信息 (name, namespace, UID)
   - PID 和状态正常

2. **镜像列表** ✅
   - 发现 24 个镜像
   - 包括 kube-apiserver, kube-controller-manager, etcd, coredns 等
   - 大小和创建时间正确显示

3. **Pod 列表** ✅
   - 发现 9 个 Pod
   - 包括 kube-system 和 local-path-storage 命名空间
   - 每个 Pod 正确显示关联容器

4. **容器详情** ✅
   - 基本信息完整
   - CGroup v2 信息正确读取
   - 挂载信息 (25 个挂载)
   - 进程数量统计

**遇到的问题**:
1. macOS Mach-O 格式不兼容 Linux → 使用 GOOS=linux 交叉编译
2. /tmp 目录权限问题 → 改用 /usr/local/bin
3. 容器 ID 需要使用完整 ID → 已记录

**交叉编译命令**:
```bash
GOOS=linux GOARCH=arm64 go build -o bin/cray-linux ./cmd/cray
```

---

### Phase 4: 主界面 - 列表视图 ✅

**完成**:
- ✅ 可复用表格组件 (`pkg/ui/components/table.go`)
  - 固定表头、行选择、列宽控制
  - 行颜色设置、列定义动态切换
- ✅ 容器列表视图 (`pkg/ui/views/container_list.go`)
  - 基础列: ID, Name, Image, Status, PID, Age
  - 扩展列: Pod Name, Namespace (按 `e` 切换)
  - 状态颜色: running=绿, paused=黄, stopped=红, created=青
- ✅ 镜像列表视图 (`pkg/ui/views/image_list.go`)
  - 列: Name, Digest, Size, Created
- ✅ Pod 列表视图 (`pkg/ui/views/pod_list.go`)
  - 列: Name, Namespace, UID, Containers (running/total)
  - 颜色表示 Pod 内容器就绪状态
- ✅ 主视图 Tab 切换 (`pkg/ui/views/main_view.go`)
  - 数字键 1/2/3 直接切换
  - Tab/Shift+Tab 循环切换
  - 高亮 Tab 指示器
- ✅ 自动刷新 (3 秒间隔) + 手动刷新 (r 键)
- ✅ 全局快捷键 (q 退出, Ctrl+C 退出)
- ✅ 每个视图的状态栏统计信息
- ✅ app.go 重写，集成 MainView
- ✅ main.go 新增 runTUI() 函数启动 TUI 模式
- ✅ 编译成功 (`go build ./...`)
- ✅ `go vet` 无问题
- ✅ 所有已有测试通过

**新增文件**:
```
pkg/ui/components/table.go
pkg/ui/views/container_list.go
pkg/ui/views/image_list.go
pkg/ui/views/pod_list.go
pkg/ui/views/main_view.go
```

**修改文件**:
```
pkg/ui/app.go
cmd/cray/main.go
```

---

### Phase 5: 容器详情页 - 概览信息 ✅

**完成**:
- ✅ 信息面板组件 (`pkg/ui/components/info_panel.go`)
  - InfoItem: key-value 显示，支持自定义颜色
  - InfoSection: 分组显示，带标题
  - 动态颜色标签转换
- ✅ 容器详情视图 (`pkg/ui/views/container_detail.go`)
  - 上下分割布局: 标题栏 + 概览面板 + Tab栏 + Tab内容 + 状态栏
  - 概览面板 5 个分区:
    - Basic: ID, Name, Status, Created, Started, Uptime
    - Process: Host PID, Process Count, Shim PID
    - CGroup: Path, Version, CPU/Memory/PIDs Limits
    - Image: Name, Read-Only Layers, RO/RW Layer Paths, Mount Count
    - Pod: Name, Namespace, UID (仅 K8s 容器显示)
  - Tab 栏预留: Top(1), Processes(2), Mounts(3), Layers(4), Runtime(5)
- ✅ 导航逻辑 (`pkg/ui/app.go`)
  - 容器列表 Enter → 详情页
  - 详情页 Esc/q → 返回列表
  - 详情页内 r 刷新, 1-5 切换 Tab
- ✅ 编译、vet、测试全部通过

**新增文件**:
```
pkg/ui/components/info_panel.go (78 行)
pkg/ui/views/container_detail.go (278 行)
```

**修改文件**:
```
pkg/ui/app.go
```

---

## 待办事项

- [x] Phase 1: 项目架构设计与基础框架
- [x] Phase 2: Containerd 运行时实现
- [x] Phase 3: 系统信息采集模块
- [x] Phase 4: 主界面 - 列表视图
- [x] Phase 5: 容器详情页 - 概览信息
- [x] Phase 6: 容器详情页 - Top 视图
- [x] Phase 7: 容器详情页 - 进程树视图
- [x] Phase 8: 容器详情页 - 挂载卷视图
- [x] Phase 9: 容器详情页 - 镜像层视图
- [x] Phase 10: 容器详情页 - 运行信息视图
- [x] Phase 11: 详情页 Tab 切换与集成
- [ ] Phase 12: 性能优化与用户体验
- [ ] Phase 13: 测试与文档
- [ ] Phase 14: 打包与发布

---

## 技术决策记录

### TD-001: TUI 框架选择 tview
**日期**: 2026-03-10
**决策**: 使用 tview 作为 TUI 框架
**理由**:
- 成熟稳定，社区活跃
- 组件丰富 (Table, TreeView, Flex 等)
- 良好的布局系统
- 支持鼠标和键盘交互
**替代方案**: bubbletea (更现代但学习曲线陡)

### TD-002: 运行时抽象接口
**日期**: 2026-03-10
**决策**: 定义 Runtime 接口抽象不同容器运行时
**理由**:
- 解耦 UI 和运行时实现
- 为未来支持 cri-o, docker 做准备
- 便于测试 (可 mock)
**实现**: pkg/runtime/interface.go

---

## 测试记录

_暂无测试记录_

---

## 性能指标

_待收集_

---

## 问题与解决

_暂无问题记录_
